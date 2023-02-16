package foreign_keys

import (
	"fmt"
	"sort"
	"strings"

	"github.com/google/go-cmp/cmp"

	"github.com/latolukasz/beeorm/v2"
)

const PluginCode = "github.com/latolukasz/beeorm/plugins/foreign_keys"
const defaultTagName = "fk"
const fkColumnsOption = "fk-columns"

type Plugin struct {
	options *Options
}
type Options struct {
	TagName string
}

type foreignIndex struct {
	Column         string
	Table          string
	ParentDatabase string
	OnDelete       string
}

type foreignKeyDB struct {
	ConstraintName         string
	ColumnName             string
	ReferencedTableName    string
	ReferencedEntitySchema string
	OnDelete               string
}

func Init(options *Options) *Plugin {
	if options == nil {
		options = &Options{}
	}
	if options.TagName == "" {
		options.TagName = defaultTagName
	}
	return &Plugin{options}
}

func (p *Plugin) GetCode() string {
	return PluginCode
}

func (p *Plugin) InterfaceInitEntitySchema(schema beeorm.SettableEntitySchema, _ *beeorm.Registry) error {
	refs := schema.GetReferences()
	if len(refs) == 0 {
		return nil
	}
	globalFK := schema.GetTag("ORM", p.options.TagName, "true", "") == "true"
	fkList := make([]string, 0)
	for _, column := range refs {
		columnTag := schema.GetTag(column, p.options.TagName, "true", "")
		if globalFK && columnTag != "skip" {
			fkList = append(fkList, column)
		} else if columnTag == "true" {
			fkList = append(fkList, column)
		}
	}
	if len(fkList) > 0 {
		schema.SetPluginOption(PluginCode, fkColumnsOption, fkList)
	}
	return nil
}

func (p *Plugin) PluginInterfaceTableSQLSchemaDefinition(engine beeorm.Engine, sqlSchema *beeorm.TableSQLSchemaDefinition) error {
	refs := sqlSchema.EntitySchema.GetPluginOption(PluginCode, fkColumnsOption)
	if refs == nil {
		return nil
	}
	refsMap := refs.([]string)
	addForeignKeys := make(map[string]*foreignIndex)
	dropForeignKeys := make(map[string]*foreignIndex)
	for _, refColumn := range refsMap {
		field, _ := sqlSchema.EntitySchema.GetType().FieldByName(refColumn)
		refOneSchema := engine.GetRegistry().GetEntitySchema(field.Type.Elem().String())
		pool := refOneSchema.GetMysql(engine)
		foreignKey := &foreignIndex{Column: refColumn, Table: refOneSchema.GetTableName(),
			ParentDatabase: pool.GetPoolConfig().GetDatabase(), OnDelete: "RESTRICT"}
		name := fmt.Sprintf("%s:%s:%s", pool.GetPoolConfig().GetDatabase(), sqlSchema.EntitySchema.GetType(), refColumn)
		addForeignKeys[name] = foreignKey
		hasIndex := false
		for _, index := range sqlSchema.EntityIndexes {
			if index.GetColumns()[0] == refColumn {
				hasIndex = true
				break
			}
		}
		if !hasIndex {
			index := &beeorm.IndexSchemaDefinition{Name: refColumn + "Ref", Unique: false}
			index.SetColumns([]string{refColumn})
			sqlSchema.EntityIndexes = append(sqlSchema.EntityIndexes, index)
		}
	}
	var dbForeignKeys map[string]*foreignIndex
	if sqlSchema.DBCreateSchema != "" {
		dbForeignKeys = getForeignKeys(engine, sqlSchema)
		for name, fk := range dbForeignKeys {
			current, hasCurrent := addForeignKeys[name]
			if !hasCurrent {
				dropForeignKeys[name] = fk
				continue
			}
			if cmp.Equal(current, fk) {
				delete(addForeignKeys, name)
			} else {
				dropForeignKeys[name] = fk
			}
		}
	}
	if len(addForeignKeys) == 0 && len(dropForeignKeys) == 0 {
		return nil
	}
	alterSQL := fmt.Sprintf("ALTER TABLE `%s`.`%s`\n", sqlSchema.EntitySchema.GetMysql(engine).GetPoolConfig().GetDatabase(), sqlSchema.EntitySchema.GetTableName())

	if len(dropForeignKeys) > 0 {
		oldForeignKeys := make([]string, 0)
		dropForeignKeysSQL := alterSQL
		for keyName := range dropForeignKeys {
			oldForeignKeys = append(oldForeignKeys, buildDropForeignKeySQL(keyName))
		}
		sort.Strings(oldForeignKeys)
		for i, value := range oldForeignKeys {
			dropForeignKeysSQL += value
			if i == len(oldForeignKeys)-1 {
				dropForeignKeysSQL += ";"
			} else {
				dropForeignKeysSQL += ",\n"
			}
		}
		sqlSchema.PreAlters = append(sqlSchema.PreAlters, beeorm.Alter{
			SQL:  dropForeignKeysSQL,
			Safe: true,
			Pool: sqlSchema.EntitySchema.GetMysqlPool(),
		})
	}

	if len(addForeignKeys) > 0 {
		newForeignKeys := make([]string, 0)
		addForeignKeysSQL := alterSQL
		for keyName, foreignKey := range addForeignKeys {
			newForeignKeys = append(newForeignKeys, buildCreateForeignKeySQL(keyName, foreignKey))
		}
		sort.Strings(newForeignKeys)
		for i, value := range newForeignKeys {
			addForeignKeysSQL += value
			if i == len(newForeignKeys)-1 {
				addForeignKeysSQL += ";"
			} else {
				addForeignKeysSQL += ",\n"
			}
		}
		sqlSchema.PostAlters = append(sqlSchema.PostAlters, beeorm.Alter{
			SQL:  addForeignKeysSQL,
			Safe: true,
			Pool: sqlSchema.EntitySchema.GetMysqlPool(),
		})
	}
	return nil
}

func buildCreateForeignKeySQL(keyName string, definition *foreignIndex) string {
	return fmt.Sprintf("ADD CONSTRAINT `%s` FOREIGN KEY (`%s`) REFERENCES `%s`.`%s` (`ID`) ON DELETE %s",
		keyName, definition.Column, definition.ParentDatabase, definition.Table, definition.OnDelete)
}

func buildDropForeignKeySQL(keyName string) string {
	return fmt.Sprintf("DROP FOREIGN KEY `%s`", keyName)
}

func getForeignKeys(engine beeorm.Engine, sqlSchema *beeorm.TableSQLSchemaDefinition) map[string]*foreignIndex {
	var rows2 []foreignKeyDB
	query := "SELECT CONSTRAINT_NAME, COLUMN_NAME, REFERENCED_TABLE_NAME, REFERENCED_TABLE_SCHEMA " +
		"FROM INFORMATION_SCHEMA.KEY_COLUMN_USAGE WHERE REFERENCED_TABLE_SCHEMA IS NOT NULL " +
		"AND TABLE_SCHEMA = '%s' AND TABLE_NAME = '%s'"
	pool := sqlSchema.EntitySchema.GetMysql(engine)
	results, def := pool.Query(fmt.Sprintf(query, pool.GetPoolConfig().GetDatabase(), sqlSchema.EntitySchema.GetTableName()))
	defer def()
	for results.Next() {
		var row foreignKeyDB
		results.Scan(&row.ConstraintName, &row.ColumnName, &row.ReferencedTableName, &row.ReferencedEntitySchema)
		row.OnDelete = "RESTRICT"
		for _, line := range strings.Split(sqlSchema.DBCreateSchema, "\n") {
			line = strings.TrimSpace(strings.TrimRight(line, ","))
			if strings.Index(line, fmt.Sprintf("CONSTRAINT `%s`", row.ConstraintName)) == 0 {
				words := strings.Split(line, " ")
				if strings.ToUpper(words[len(words)-2]) == "DELETE" {
					row.OnDelete = strings.ToUpper(words[len(words)-1])
				}
			}
		}
		rows2 = append(rows2, row)
	}
	def()
	var foreignKeysDB = make(map[string]*foreignIndex)
	for _, value := range rows2 {
		foreignKey := &foreignIndex{ParentDatabase: value.ReferencedEntitySchema, Table: value.ReferencedTableName,
			Column: value.ColumnName, OnDelete: value.OnDelete}
		foreignKeysDB[value.ConstraintName] = foreignKey
	}
	return foreignKeysDB
}
