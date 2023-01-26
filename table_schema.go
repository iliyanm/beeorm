package beeorm

import (
	"crypto/sha256"
	"database/sql"
	"fmt"
	"hash/fnv"
	"reflect"
	"regexp"
	"strconv"
	"strings"
)

type CachedQuery struct{}

type cachedQueryDefinition struct {
	Max           int
	Query         string
	TrackedFields []string
	QueryFields   []string
	OrderFields   []string
}

type Enum interface {
	GetFields() []string
	GetDefault() string
	Has(value string) bool
	Index(value string) int
}

type enum struct {
	fields       []string
	mapping      map[string]int
	defaultValue string
}

func (enum *enum) GetFields() []string {
	return enum.fields
}

func (enum *enum) GetDefault() string {
	return enum.defaultValue
}

func (enum *enum) Has(value string) bool {
	_, has := enum.mapping[value]
	return has
}

func (enum *enum) Index(value string) int {
	return enum.mapping[value]
}

func initEnum(ref interface{}, defaultValue ...string) *enum {
	enum := &enum{}
	e := reflect.ValueOf(ref)
	enum.mapping = make(map[string]int)
	enum.fields = make([]string, 0)
	for i := 0; i < e.Type().NumField(); i++ {
		name := e.Field(i).String()
		enum.fields = append(enum.fields, name)
		enum.mapping[name] = i + 1
	}
	if len(defaultValue) > 0 {
		enum.defaultValue = defaultValue[0]
	} else {
		enum.defaultValue = enum.fields[0]
	}
	return enum
}

type TableSchema interface {
	GetTableName() string
	GetType() reflect.Type
	NewEntity() Entity
	DropTable(engine Engine)
	TruncateTable(engine Engine)
	UpdateSchema(engine Engine)
	UpdateSchemaAndTruncateTable(engine Engine)
	GetMysql(engine Engine) *DB
	GetLocalCache(engine Engine) (cache LocalCache, has bool)
	GetRedisCache(engine Engine) (cache RedisCache, has bool)
	GetReferences() []string
	GetColumns() []string
	GetUniqueIndexes() map[string][]string
	GetSchemaChanges(engine Engine) (has bool, alters []Alter)
	GetUsage(registry ValidatedRegistry) map[reflect.Type][]string
	GetTag(field, key, trueValue, defaultValue string) string
	GetOption(plugin, key string) interface{}
	GetOptionString(plugin, key string) string
}

type SettableTableSchema interface {
	TableSchema
	SetOption(plugin, key string, value interface{})
}

type tableSchema struct {
	tableName                  string
	mysqlPoolName              string
	t                          reflect.Type
	fields                     *tableFields
	registry                   *validatedRegistry
	fieldsQuery                string
	tags                       map[string]map[string]string
	cachedIndexes              map[string]*cachedQueryDefinition
	cachedIndexesOne           map[string]*cachedQueryDefinition
	cachedIndexesAll           map[string]*cachedQueryDefinition
	cachedIndexesTrackedFields map[string]bool
	columnNames                []string
	columnMapping              map[string]int
	uniqueIndices              map[string][]string
	uniqueIndicesGlobal        map[string][]string
	refOne                     []string
	localCacheName             string
	hasLocalCache              bool
	redisCacheName             string
	hasRedisCache              bool
	searchCacheName            string
	cachePrefix                string
	structureHash              uint64
	hasFakeDelete              bool
	hasSearchableFakeDelete    bool
	skipLogs                   []string
	hasUUID                    bool
	mapBindToScanPointer       mapBindToScanPointer
	mapPointerToValue          mapPointerToValue
	options                    map[string]map[string]interface{}
}

type mapBindToScanPointer map[string]func() interface{}
type mapPointerToValue map[string]func(val interface{}) interface{}

type tableFields struct {
	t                       reflect.Type
	fields                  map[int]reflect.StructField
	prefix                  string
	uintegers               []int
	integers                []int
	uintegersNullable       []int
	uintegersNullableSize   []int
	integersNullable        []int
	integersNullableSize    []int
	strings                 []int
	stringsEnums            []int
	enums                   []Enum
	sliceStringsSets        []int
	sets                    []Enum
	bytes                   []int
	fakeDelete              int
	booleans                []int
	booleansNullable        []int
	floats                  []int
	floatsPrecision         []int
	floatsNullable          []int
	floatsNullablePrecision []int
	floatsNullableSize      []int
	timesNullable           []int
	datesNullable           []int
	times                   []int
	dates                   []int
	jsons                   []int
	structs                 []int
	structsFields           []*tableFields
	refs                    []int
	refsTypes               []reflect.Type
}

func getTableSchema(registry *validatedRegistry, entityType reflect.Type) *tableSchema {
	return registry.tableSchemas[entityType]
}

func (tableSchema *tableSchema) GetTableName() string {
	return tableSchema.tableName
}

func (tableSchema *tableSchema) GetType() reflect.Type {
	return tableSchema.t
}

func (tableSchema *tableSchema) DropTable(engine Engine) {
	pool := tableSchema.GetMysql(engine)
	pool.Exec(fmt.Sprintf("DROP TABLE IF EXISTS `%s`.`%s`;", pool.GetPoolConfig().GetDatabase(), tableSchema.tableName))
}

func (tableSchema *tableSchema) TruncateTable(engine Engine) {
	pool := tableSchema.GetMysql(engine)
	_ = pool.Exec(fmt.Sprintf("DELETE FROM `%s`.`%s`", pool.GetPoolConfig().GetDatabase(), tableSchema.tableName))
	_ = pool.Exec(fmt.Sprintf("ALTER TABLE `%s`.`%s` AUTO_INCREMENT = 1", pool.GetPoolConfig().GetDatabase(), tableSchema.tableName))
}

func (tableSchema *tableSchema) UpdateSchema(engine Engine) {
	pool := tableSchema.GetMysql(engine)
	has, alters := tableSchema.GetSchemaChanges(engine)
	if has {
		for _, alter := range alters {
			_ = pool.Exec(alter.SQL)
		}
	}
}

func (tableSchema *tableSchema) UpdateSchemaAndTruncateTable(engine Engine) {
	tableSchema.UpdateSchema(engine)
	pool := tableSchema.GetMysql(engine)
	_ = pool.Exec(fmt.Sprintf("DELETE FROM `%s`.`%s`", pool.GetPoolConfig().GetDatabase(), tableSchema.tableName))
	_ = pool.Exec(fmt.Sprintf("ALTER TABLE `%s`.`%s` AUTO_INCREMENT = 1", pool.GetPoolConfig().GetDatabase(), tableSchema.tableName))
}

func (tableSchema *tableSchema) GetMysql(engine Engine) *DB {
	return engine.GetMysql(tableSchema.mysqlPoolName)
}

func (tableSchema *tableSchema) GetLocalCache(engine Engine) (cache LocalCache, has bool) {
	if !tableSchema.hasLocalCache {
		return nil, false
	}
	return engine.GetLocalCache(tableSchema.localCacheName), true
}

func (tableSchema *tableSchema) GetRedisCache(engine Engine) (cache RedisCache, has bool) {
	if !tableSchema.hasRedisCache {
		return nil, false
	}
	return engine.GetRedis(tableSchema.redisCacheName), true
}

func (tableSchema *tableSchema) GetReferences() []string {
	return tableSchema.refOne
}

func (tableSchema *tableSchema) GetColumns() []string {
	return tableSchema.columnNames
}

func (tableSchema *tableSchema) GetUniqueIndexes() map[string][]string {
	data := make(map[string][]string)
	for k, v := range tableSchema.uniqueIndices {
		data[k] = v
	}
	for k, v := range tableSchema.uniqueIndicesGlobal {
		data[k] = v
	}
	return data
}

func (tableSchema *tableSchema) GetSchemaChanges(engine Engine) (has bool, alters []Alter) {
	return getSchemaChanges(engine.(*engineImplementation), tableSchema)
}

func (tableSchema *tableSchema) GetUsage(registry ValidatedRegistry) map[reflect.Type][]string {
	vRegistry := registry.(*validatedRegistry)
	results := make(map[reflect.Type][]string)
	if vRegistry.entities != nil {
		for _, t := range vRegistry.entities {
			schema := getTableSchema(vRegistry, t)
			tableSchema.getUsage(schema.fields, schema.t, "", results)
		}
	}
	return results
}

func (tableSchema *tableSchema) getUsage(fields *tableFields, t reflect.Type, prefix string, results map[reflect.Type][]string) {
	tName := tableSchema.t.String()
	for i, fieldID := range fields.refs {
		if fields.refsTypes[i].String() == tName {
			results[t] = append(results[t], prefix+fields.t.Field(fieldID).Name)
		}
	}
	for i, k := range fields.structs {
		f := fields.t.Field(k)
		subPrefix := prefix
		if !f.Anonymous {
			subPrefix += f.Name
		}
		tableSchema.getUsage(fields.structsFields[i], t, subPrefix, results)
	}
}

func (tableSchema *tableSchema) init(registry *Registry, entityType reflect.Type) error {
	tableSchema.t = entityType
	tableSchema.tags = extractTags(registry, entityType, "")
	oneRefs := make([]string, 0)
	tableSchema.mapBindToScanPointer = mapBindToScanPointer{}
	tableSchema.mapPointerToValue = mapPointerToValue{}
	tableSchema.mysqlPoolName = tableSchema.getTag("mysql", "default", "default")
	_, has := registry.mysqlPools[tableSchema.mysqlPoolName]
	if !has {
		return fmt.Errorf("mysql pool '%s' not found", tableSchema.mysqlPoolName)
	}
	tableSchema.tableName = tableSchema.getTag("table", entityType.Name(), entityType.Name())
	localCache := tableSchema.getTag("localCache", "default", "")
	redisCache := tableSchema.getTag("redisCache", "default", "")
	if localCache != "" {
		_, has = registry.localCachePools[localCache]
		if !has {
			return fmt.Errorf("local cache pool '%s' not found", localCache)
		}
	}
	if redisCache != "" {
		_, has = registry.mysqlPools[redisCache]
		if !has {
			return fmt.Errorf("redis pool '%s' not found", redisCache)
		}
	}
	cachePrefix := ""
	if tableSchema.mysqlPoolName != "default" {
		cachePrefix = tableSchema.mysqlPoolName
	}
	cachePrefix += tableSchema.tableName
	cachedQueries := make(map[string]*cachedQueryDefinition)
	cachedQueriesOne := make(map[string]*cachedQueryDefinition)
	cachedQueriesAll := make(map[string]*cachedQueryDefinition)
	cachedQueriesTrackedFields := make(map[string]bool)
	fakeDeleteField, has := entityType.FieldByName("FakeDelete")
	if has && fakeDeleteField.Type.String() == "bool" {
		tableSchema.hasFakeDelete = true
		searchable := tableSchema.tags["FakeDelete"] != nil && tableSchema.tags["FakeDelete"]["searchable"] == "true"
		tableSchema.hasSearchableFakeDelete = searchable
	}
	for key, values := range tableSchema.tags {
		isOne := false
		query, has := values["query"]
		if !has {
			query, has = values["queryOne"]
			isOne = true
		}
		queryOrigin := query
		fields := make([]string, 0)
		fieldsTracked := make([]string, 0)
		fieldsQuery := make([]string, 0)
		fieldsOrder := make([]string, 0)
		if has {
			re := regexp.MustCompile(":([A-Za-z\\d])+")
			variables := re.FindAllString(query, -1)
			for _, variable := range variables {
				fieldName := variable[1:]
				has := false
				for _, v := range fields {
					if v == fieldName {
						has = true
						break
					}
				}
				if !has {
					fields = append(fields, fieldName)
				}
				query = strings.Replace(query, variable, fmt.Sprintf("`%s`", fieldName), 1)
			}
			if tableSchema.hasFakeDelete && len(variables) > 0 {
				fields = append(fields, "FakeDelete")
			}
			if query == "" {
				if tableSchema.hasFakeDelete {
					query = "`FakeDelete` = 0 ORDER BY `ID`"
				} else {
					query = "1 ORDER BY `ID`"
				}
			} else if tableSchema.hasFakeDelete {
				query = "`FakeDelete` = 0 AND " + query
			}
			queryLower := strings.ToLower(queryOrigin)
			posOrderBy := strings.Index(queryLower, "order by")
			for _, f := range fields {
				if f != "ID" {
					fieldsTracked = append(fieldsTracked, f)
				}
				pos := strings.Index(queryOrigin, ":"+f)
				if pos < posOrderBy || posOrderBy == -1 {
					fieldsQuery = append(fieldsQuery, f)
				}
			}
			if posOrderBy > -1 {
				variables = re.FindAllString(queryOrigin[posOrderBy:], -1)
				for _, variable := range variables {
					fieldName := variable[1:]
					fieldsOrder = append(fieldsOrder, fieldName)
				}
			}

			if !isOne {
				def := &cachedQueryDefinition{50000, query, fieldsTracked, fieldsQuery, fieldsOrder}
				cachedQueries[key] = def
				cachedQueriesAll[key] = def
			} else {
				def := &cachedQueryDefinition{1, query, fieldsTracked, fieldsQuery, fieldsOrder}
				cachedQueriesOne[key] = def
				cachedQueriesAll[key] = def
			}
			for _, name := range fieldsTracked {
				cachedQueriesTrackedFields[name] = true
			}
		}
		_, has = values["ref"]
		if has {
			oneRefs = append(oneRefs, key)
		}
	}
	for _, plugin := range registry.plugins {
		interfaceInitTableSchema, isInterfaceInitTableSchema := plugin.(PluginInterfaceInitTableSchema)
		if isInterfaceInitTableSchema {
			err := interfaceInitTableSchema.InterfaceInitTableSchema(tableSchema, registry)
			if err != nil {
				return err
			}
		}
	}
	hasUUID := tableSchema.getTag("uuid", "true", "false") == "true"
	if hasUUID {
		idField, is := entityType.FieldByName("ID")
		if is && idField.Type.String() != "uint64" {
			return fmt.Errorf("entity %s with uuid enabled must be unit64", entityType.String())
		}
	}
	uniqueIndices := make(map[string]map[int]string)
	uniqueIndicesSimple := make(map[string][]string)
	uniqueIndicesSimpleGlobal := make(map[string][]string)
	indices := make(map[string]map[int]string)
	uniqueGlobal := tableSchema.getTag("unique", "", "")
	if uniqueGlobal != "" {
		parts := strings.Split(uniqueGlobal, "|")
		for _, part := range parts {
			def := strings.Split(part, ":")
			uniqueIndices[def[0]] = make(map[int]string)
			uniqueIndicesSimple[def[0]] = make([]string, 0)
			uniqueIndicesSimpleGlobal[def[0]] = make([]string, 0)
			for i, field := range strings.Split(def[1], ",") {
				uniqueIndices[def[0]][i+1] = field
				uniqueIndicesSimple[def[0]] = append(uniqueIndicesSimple[def[0]], field)
				uniqueIndicesSimpleGlobal[def[0]] = append(uniqueIndicesSimpleGlobal[def[0]], field)
			}
		}
	}
	for k, v := range tableSchema.tags {
		keys, has := v["unique"]
		if has && k != "ORM" {
			values := strings.Split(keys, ",")
			for _, indexName := range values {
				parts := strings.Split(indexName, ":")
				id := int64(1)
				if len(parts) > 1 {
					id, _ = strconv.ParseInt(parts[1], 10, 64)
				}
				if uniqueIndices[parts[0]] == nil {
					uniqueIndices[parts[0]] = make(map[int]string)
				}
				uniqueIndices[parts[0]][int(id)] = k
				if uniqueIndicesSimple[parts[0]] == nil {
					uniqueIndicesSimple[parts[0]] = make([]string, 0)
				}
				uniqueIndicesSimple[parts[0]] = append(uniqueIndicesSimple[parts[0]], k)
			}
		}
		keys, has = v["index"]
		if has {
			values := strings.Split(keys, ",")
			for _, indexName := range values {
				parts := strings.Split(indexName, ":")
				id := int64(1)
				if len(parts) > 1 {
					id, _ = strconv.ParseInt(parts[1], 10, 64)
				}
				if indices[parts[0]] == nil {
					indices[parts[0]] = make(map[int]string)
				}
				indices[parts[0]][int(id)] = k
			}
		}
	}
	for _, ref := range oneRefs {
		has := false
		for _, v := range indices {
			if v[1] == ref {
				has = true
				break
			}
		}
		if !has {
			for _, v := range uniqueIndices {
				if v[1] == ref {
					has = true
					break
				}
			}
			if !has {
				indices["_"+ref] = map[int]string{1: ref}
			}
		}
	}
	tableSchema.fields = tableSchema.buildTableFields(entityType, registry, 1, "", tableSchema.tags)
	tableSchema.columnNames, tableSchema.fieldsQuery = tableSchema.fields.buildColumnNames("")
	columnMapping := make(map[string]int)
	for i, name := range tableSchema.columnNames {
		columnMapping[name] = i
	}
	cachePrefix = fmt.Sprintf("%x", sha256.Sum256([]byte(cachePrefix+tableSchema.fieldsQuery)))
	cachePrefix = cachePrefix[0:5]
	h := fnv.New32a()
	_, _ = h.Write([]byte(cachePrefix))

	tableSchema.structureHash = uint64(h.Sum32())
	tableSchema.columnMapping = columnMapping
	tableSchema.cachedIndexes = cachedQueries
	tableSchema.cachedIndexesOne = cachedQueriesOne
	tableSchema.cachedIndexesAll = cachedQueriesAll
	tableSchema.cachedIndexesTrackedFields = cachedQueriesTrackedFields
	tableSchema.localCacheName = localCache
	tableSchema.hasLocalCache = localCache != ""
	tableSchema.redisCacheName = redisCache
	tableSchema.hasRedisCache = redisCache != ""
	tableSchema.refOne = oneRefs
	tableSchema.cachePrefix = cachePrefix
	tableSchema.uniqueIndices = uniqueIndicesSimple
	tableSchema.uniqueIndicesGlobal = uniqueIndicesSimpleGlobal
	tableSchema.hasUUID = hasUUID
	return tableSchema.validateIndexes(uniqueIndices, indices)
}

func (tableSchema *tableSchema) validateIndexes(uniqueIndices map[string]map[int]string, indices map[string]map[int]string) error {
	all := make(map[string]map[int]string)
	for k, v := range uniqueIndices {
		all[k] = v
	}
	for k, v := range indices {
		all[k] = v
	}
	for k, v := range all {
		for k2, v2 := range all {
			if k == k2 {
				continue
			}
			same := 0
			for i := 1; i <= len(v); i++ {
				right, has := v2[i]
				if has && right == v[i] {
					same++
					continue
				}
				break
			}
			if same == len(v) {
				return fmt.Errorf("duplicated index %s with %s in %s", k, k2, tableSchema.t.String())
			}
		}
	}
	for k, v := range tableSchema.cachedIndexesOne {
		ok := false
		for _, columns := range uniqueIndices {
			if len(columns) != len(v.QueryFields) {
				continue
			}
			valid := 0
			for _, field1 := range v.QueryFields {
				for _, field2 := range columns {
					if field1 == field2 {
						valid++
					}
				}
			}
			if valid == len(columns) {
				ok = true
			}
		}
		if !ok {
			return fmt.Errorf("missing unique index for cached query '%s' in %s", k, tableSchema.t.String())
		}
	}
	for k, v := range tableSchema.cachedIndexes {
		if v.Query == "1 ORDER BY `ID`" {
			continue
		}
		//first do we have query fields
		ok := false
		for _, columns := range all {
			valid := 0
			for _, field1 := range v.QueryFields {
				for _, field2 := range columns {
					if field1 == field2 {
						valid++
					}
				}
			}
			if valid == len(v.QueryFields) {
				if len(v.OrderFields) == 0 {
					ok = true
					break
				}
				valid := 0
				key := len(columns)
				if columns[len(columns)] == "FakeDelete" {
					key--
				}
				for i := len(v.OrderFields); i > 0; i-- {
					if columns[key] == v.OrderFields[i-1] {
						valid++
						key--
						continue
					}
					break
				}
				if valid == len(v.OrderFields) {
					ok = true
				}
			}
		}
		if !ok {
			return fmt.Errorf("missing index for cached query '%s' in %s", k, tableSchema.t.String())
		}
	}
	return nil
}

func (tableSchema *tableSchema) getTag(key, trueValue, defaultValue string) string {
	userValue, has := tableSchema.tags["ORM"][key]
	if has {
		if userValue == "true" {
			return trueValue
		}
		return userValue
	}
	return tableSchema.GetTag("ORM", key, trueValue, defaultValue)
}

func (tableSchema *tableSchema) GetTag(field, key, trueValue, defaultValue string) string {
	userValue, has := tableSchema.tags[field][key]
	if has {
		if userValue == "true" {
			return trueValue
		}
		return userValue
	}
	return defaultValue
}

func (tableSchema *tableSchema) GetTagBool(field, key string) bool {
	tag := tableSchema.GetTag(field, key, "1", "")
	return tag == "1"
}

func (tableSchema *tableSchema) GetOption(plugin, key string) interface{} {
	if tableSchema.options == nil {
		return nil
	}
	values, has := tableSchema.options[plugin]
	if !has {
		return nil
	}
	return values[key]
}

func (tableSchema *tableSchema) GetOptionString(plugin, key string) string {
	val := tableSchema.GetOption(plugin, key)
	if val == nil {
		return ""
	}
	return val.(string)
}

func (tableSchema *tableSchema) SetOption(plugin, key string, value interface{}) {
	if tableSchema.options == nil {
		tableSchema.options = map[string]map[string]interface{}{plugin: {key: value}}
	} else {
		before, has := tableSchema.options[plugin]
		if !has {
			tableSchema.options[plugin] = map[string]interface{}{key: value}
		} else {
			before[key] = value
		}
	}
}

func (tableSchema *tableSchema) buildTableFields(t reflect.Type, registry *Registry,
	start int, prefix string, schemaTags map[string]map[string]string) *tableFields {
	fields := &tableFields{t: t, prefix: prefix, fields: make(map[int]reflect.StructField)}
	for i := start; i < t.NumField(); i++ {
		f := t.Field(i)
		tags := schemaTags[prefix+f.Name]
		_, has := tags["ignore"]
		if has {
			continue
		}
		attributes := schemaFieldAttributes{
			Fields:   fields,
			Tags:     tags,
			Index:    i,
			Prefix:   prefix,
			Field:    f,
			TypeName: f.Type.String(),
		}
		fields.fields[i] = f
		switch attributes.TypeName {
		case "uint",
			"uint8",
			"uint16",
			"uint32",
			"uint64":
			tableSchema.buildUintField(attributes)
		case "*uint",
			"*uint8",
			"*uint16",
			"*uint32",
			"*uint64":
			tableSchema.buildUintPointerField(attributes)
		case "int",
			"int8",
			"int16",
			"int32",
			"int64":
			tableSchema.buildIntField(attributes)
		case "*int",
			"*int8",
			"*int16",
			"*int32",
			"*int64":
			tableSchema.buildIntPointerField(attributes)
		case "string":
			tableSchema.buildStringField(attributes, registry)
		case "[]string":
			tableSchema.buildStringSliceField(attributes, registry)
		case "[]uint8":
			fields.bytes = append(fields.bytes, i)
		case "bool":
			tableSchema.buildBoolField(attributes)
		case "*bool":
			tableSchema.buildBoolPointerField(attributes)
		case "float32",
			"float64":
			tableSchema.buildFloatField(attributes)
		case "*float32",
			"*float64":
			tableSchema.buildFloatPointerField(attributes)
		case "*beeorm.CachedQuery":
			continue
		case "*time.Time":
			tableSchema.buildTimePointerField(attributes)
		case "time.Time":
			tableSchema.buildTimeField(attributes)
		default:
			k := f.Type.Kind().String()
			if k == "struct" {
				tableSchema.buildStructField(attributes, registry, schemaTags)
			} else if k == "ptr" {
				tableSchema.buildPointerField(attributes)
			} else {
				tableSchema.buildPointersSliceField(attributes)
			}
		}
	}
	return fields
}

type schemaFieldAttributes struct {
	Field    reflect.StructField
	TypeName string
	Tags     map[string]string
	Fields   *tableFields
	Index    int
	Prefix   string
}

func (attributes schemaFieldAttributes) GetColumnName() string {
	return attributes.Prefix + attributes.Field.Name
}

func (tableSchema *tableSchema) buildUintField(attributes schemaFieldAttributes) {
	attributes.Fields.uintegers = append(attributes.Fields.uintegers, attributes.Index)
	columnName := attributes.GetColumnName()
	tableSchema.mapBindToScanPointer[columnName] = func() interface{} {
		v := uint64(0)
		return &v
	}
	tableSchema.mapPointerToValue[columnName] = func(val interface{}) interface{} {
		return *val.(*uint64)
	}
}

func (tableSchema *tableSchema) buildUintPointerField(attributes schemaFieldAttributes) {
	attributes.Fields.uintegersNullable = append(attributes.Fields.uintegersNullable, attributes.Index)
	columnName := attributes.GetColumnName()
	switch attributes.TypeName {
	case "*uint":
		attributes.Fields.uintegersNullableSize = append(attributes.Fields.uintegersNullableSize, 0)
	case "*uint8":
		attributes.Fields.uintegersNullableSize = append(attributes.Fields.uintegersNullableSize, 8)
	case "*uint16":
		attributes.Fields.uintegersNullableSize = append(attributes.Fields.uintegersNullableSize, 16)
	case "*uint32":
		attributes.Fields.uintegersNullableSize = append(attributes.Fields.uintegersNullableSize, 32)
	case "*uint64":
		attributes.Fields.uintegersNullableSize = append(attributes.Fields.uintegersNullableSize, 64)
	}
	tableSchema.mapBindToScanPointer[columnName] = scanIntNullablePointer
	tableSchema.mapPointerToValue[columnName] = pointerUintNullableScan
}

func (tableSchema *tableSchema) buildIntField(attributes schemaFieldAttributes) {
	attributes.Fields.integers = append(attributes.Fields.integers, attributes.Index)
	columnName := attributes.GetColumnName()
	tableSchema.mapBindToScanPointer[columnName] = func() interface{} {
		v := int64(0)
		return &v
	}
	tableSchema.mapPointerToValue[columnName] = func(val interface{}) interface{} {
		return *val.(*int64)
	}
}

func (tableSchema *tableSchema) buildIntPointerField(attributes schemaFieldAttributes) {
	attributes.Fields.integersNullable = append(attributes.Fields.integersNullable, attributes.Index)
	columnName := attributes.GetColumnName()
	switch attributes.TypeName {
	case "*int":
		attributes.Fields.integersNullableSize = append(attributes.Fields.integersNullableSize, 0)
	case "*int8":
		attributes.Fields.integersNullableSize = append(attributes.Fields.integersNullableSize, 8)
	case "*int16":
		attributes.Fields.integersNullableSize = append(attributes.Fields.integersNullableSize, 16)
	case "*int32":
		attributes.Fields.integersNullableSize = append(attributes.Fields.integersNullableSize, 32)
	case "*int64":
		attributes.Fields.integersNullableSize = append(attributes.Fields.integersNullableSize, 64)
	}
	tableSchema.mapBindToScanPointer[columnName] = scanIntNullablePointer
	tableSchema.mapPointerToValue[columnName] = pointerIntNullableScan
}

func (tableSchema *tableSchema) buildStringField(attributes schemaFieldAttributes, registry *Registry) {
	enumCode, hasEnum := attributes.Tags["enum"]
	columnName := attributes.GetColumnName()
	if hasEnum {
		attributes.Fields.stringsEnums = append(attributes.Fields.stringsEnums, attributes.Index)
		attributes.Fields.enums = append(attributes.Fields.enums, registry.enums[enumCode])
	} else {
		attributes.Fields.strings = append(attributes.Fields.strings, attributes.Index)
	}
	tableSchema.mapBindToScanPointer[columnName] = func() interface{} {
		return &sql.NullString{}
	}
	tableSchema.mapPointerToValue[columnName] = func(val interface{}) interface{} {
		v := val.(*sql.NullString)
		if v.Valid {
			return v.String
		}
		return nil
	}
}

func (tableSchema *tableSchema) buildStringSliceField(attributes schemaFieldAttributes, registry *Registry) {
	setCode, hasSet := attributes.Tags["set"]
	columnName := attributes.GetColumnName()
	if hasSet {
		attributes.Fields.sliceStringsSets = append(attributes.Fields.sliceStringsSets, attributes.Index)
		attributes.Fields.sets = append(attributes.Fields.sets, registry.enums[setCode])
	} else {
		attributes.Fields.jsons = append(attributes.Fields.jsons, attributes.Index)
	}
	tableSchema.mapBindToScanPointer[columnName] = scanStringNullablePointer
	tableSchema.mapPointerToValue[columnName] = pointerStringNullableScan
}

func (tableSchema *tableSchema) buildBoolField(attributes schemaFieldAttributes) {
	columnName := attributes.GetColumnName()
	if attributes.GetColumnName() == "FakeDelete" {
		attributes.Fields.fakeDelete = attributes.Index
	} else {
		attributes.Fields.booleans = append(attributes.Fields.booleans, attributes.Index)
		tableSchema.mapBindToScanPointer[columnName] = scanBoolPointer
		tableSchema.mapPointerToValue[columnName] = pointerBoolScan
	}
}

func (tableSchema *tableSchema) buildBoolPointerField(attributes schemaFieldAttributes) {
	attributes.Fields.booleansNullable = append(attributes.Fields.booleansNullable, attributes.Index)
	columnName := attributes.GetColumnName()
	tableSchema.mapBindToScanPointer[columnName] = scanBoolNullablePointer
	tableSchema.mapPointerToValue[columnName] = pointerBoolNullableScan
}

func (tableSchema *tableSchema) buildFloatField(attributes schemaFieldAttributes) {
	columnName := attributes.GetColumnName()
	precision := 8
	if attributes.TypeName == "float32" {
		precision = 4
	}
	precisionAttribute, has := attributes.Tags["precision"]
	if has {
		userPrecision, _ := strconv.Atoi(precisionAttribute)
		precision = userPrecision
	} else {
		decimal, has := attributes.Tags["decimal"]
		if has {
			decimalArgs := strings.Split(decimal, ",")
			precision, _ = strconv.Atoi(decimalArgs[1])
		}
	}
	attributes.Fields.floats = append(attributes.Fields.floats, attributes.Index)
	attributes.Fields.floatsPrecision = append(attributes.Fields.floatsPrecision, precision)
	tableSchema.mapBindToScanPointer[columnName] = func() interface{} {
		v := float64(0)
		return &v
	}
	tableSchema.mapPointerToValue[columnName] = func(val interface{}) interface{} {
		return *val.(*float64)
	}
}

func (tableSchema *tableSchema) buildFloatPointerField(attributes schemaFieldAttributes) {
	columnName := attributes.GetColumnName()
	precision := 8
	if attributes.TypeName == "*float32" {
		precision = 4
		attributes.Fields.floatsNullableSize = append(attributes.Fields.floatsNullableSize, 32)
	} else {
		attributes.Fields.floatsNullableSize = append(attributes.Fields.floatsNullableSize, 64)
	}
	precisionAttribute, has := attributes.Tags["precision"]
	if has {
		userPrecision, _ := strconv.Atoi(precisionAttribute)
		precision = userPrecision
	} else {
		precisionAttribute, has := attributes.Tags["decimal"]
		if has {
			precision, _ = strconv.Atoi(strings.Split(precisionAttribute, ",")[1])
		}
	}
	attributes.Fields.floatsNullable = append(attributes.Fields.floatsNullable, attributes.Index)
	attributes.Fields.floatsNullablePrecision = append(attributes.Fields.floatsNullablePrecision, precision)
	tableSchema.mapBindToScanPointer[columnName] = scanFloatNullablePointer
	tableSchema.mapPointerToValue[columnName] = pointerFloatNullableScan
}

func (tableSchema *tableSchema) buildTimePointerField(attributes schemaFieldAttributes) {
	columnName := attributes.GetColumnName()
	_, hasTime := attributes.Tags["time"]
	if hasTime {
		attributes.Fields.timesNullable = append(attributes.Fields.timesNullable, attributes.Index)
	} else {
		attributes.Fields.datesNullable = append(attributes.Fields.datesNullable, attributes.Index)
	}
	tableSchema.mapBindToScanPointer[columnName] = scanStringNullablePointer
	tableSchema.mapPointerToValue[columnName] = pointerStringNullableScan
}

func (tableSchema *tableSchema) buildTimeField(attributes schemaFieldAttributes) {
	columnName := attributes.GetColumnName()
	_, hasTime := attributes.Tags["time"]
	if hasTime {
		attributes.Fields.times = append(attributes.Fields.times, attributes.Index)
	} else {
		attributes.Fields.dates = append(attributes.Fields.dates, attributes.Index)
	}
	tableSchema.mapBindToScanPointer[columnName] = scanStringPointer
	tableSchema.mapPointerToValue[columnName] = pointerStringScan
}

func (tableSchema *tableSchema) buildStructField(attributes schemaFieldAttributes, registry *Registry,
	schemaTags map[string]map[string]string) {
	attributes.Fields.structs = append(attributes.Fields.structs, attributes.Index)
	subPrefix := ""
	if !attributes.Field.Anonymous {
		subPrefix = attributes.Field.Name
	}
	subFields := tableSchema.buildTableFields(attributes.Field.Type, registry, 0, subPrefix, schemaTags)
	attributes.Fields.structsFields = append(attributes.Fields.structsFields, subFields)
}

func (tableSchema *tableSchema) buildPointerField(attributes schemaFieldAttributes) {
	columnName := attributes.GetColumnName()
	modelType := reflect.TypeOf((*Entity)(nil)).Elem()
	if attributes.Field.Type.Implements(modelType) {
		attributes.Fields.refs = append(attributes.Fields.refs, attributes.Index)
		attributes.Fields.refsTypes = append(attributes.Fields.refsTypes, attributes.Field.Type.Elem())
		tableSchema.mapBindToScanPointer[columnName] = scanIntNullablePointer
		tableSchema.mapPointerToValue[columnName] = pointerUintNullableScan
	} else {
		attributes.Fields.jsons = append(attributes.Fields.jsons, attributes.Index)
	}
}

func (tableSchema *tableSchema) buildPointersSliceField(attributes schemaFieldAttributes) {
	attributes.Fields.jsons = append(attributes.Fields.jsons, attributes.Index)
}

func extractTags(registry *Registry, entityType reflect.Type, prefix string) (fields map[string]map[string]string) {
	fields = make(map[string]map[string]string)
	for i := 0; i < entityType.NumField(); i++ {
		field := entityType.Field(i)
		for k, v := range extractTag(registry, field) {
			fields[prefix+k] = v
		}
		_, hasIgnore := fields[field.Name]["ignore"]
		if hasIgnore {
			continue
		}
		refOne := ""
		hasRef := false
		if field.Type.Kind().String() == "ptr" {
			refName := field.Type.Elem().String()
			_, hasRef = registry.entities[refName]
			if hasRef {
				refOne = refName
			}
		}

		query, hasQuery := field.Tag.Lookup("query")
		queryOne, hasQueryOne := field.Tag.Lookup("queryOne")
		if hasQuery {
			if fields[field.Name] == nil {
				fields[field.Name] = make(map[string]string)
			}
			fields[field.Name]["query"] = query
		}
		if hasQueryOne {
			if fields[field.Name] == nil {
				fields[field.Name] = make(map[string]string)
			}
			fields[field.Name]["queryOne"] = queryOne
		}
		if hasRef {
			if fields[field.Name] == nil {
				fields[field.Name] = make(map[string]string)
			}
			fields[field.Name]["ref"] = refOne
		}
	}
	return
}

func extractTag(registry *Registry, field reflect.StructField) map[string]map[string]string {
	tag, ok := field.Tag.Lookup("orm")
	if ok {
		args := strings.Split(tag, ";")
		length := len(args)
		var attributes = make(map[string]string, length)
		for j := 0; j < length; j++ {
			arg := strings.Split(args[j], "=")
			if len(arg) == 1 {
				attributes[arg[0]] = "true"
			} else {
				attributes[arg[0]] = arg[1]
			}
		}
		return map[string]map[string]string{field.Name: attributes}
	} else if field.Type.Kind().String() == "struct" {
		t := field.Type.String()
		if t != "beeorm.ORM" && t != "time.Time" {
			prefix := ""
			if !field.Anonymous {
				prefix = field.Name
			}
			return extractTags(registry, field.Type, prefix)
		}
	}
	return make(map[string]map[string]string)
}

func (tableSchema *tableSchema) getCacheKey(id uint64) string {
	return tableSchema.cachePrefix + ":" + strconv.FormatUint(id, 10)
}

func (tableSchema *tableSchema) NewEntity() Entity {
	val := reflect.New(tableSchema.t)
	e := val.Interface().(Entity)
	orm := e.getORM()
	orm.initialised = true
	orm.tableSchema = tableSchema
	orm.value = val
	orm.elem = val.Elem()
	return e
}

func (fields *tableFields) buildColumnNames(subFieldPrefix string) ([]string, string) {
	fieldsQuery := ""
	columns := make([]string, 0)
	ids := fields.uintegers
	ids = append(ids, fields.refs...)
	ids = append(ids, fields.integers...)
	ids = append(ids, fields.booleans...)
	ids = append(ids, fields.floats...)
	timesStart := len(ids)
	ids = append(ids, fields.times...)
	ids = append(ids, fields.dates...)
	timesEnd := len(ids)
	if fields.fakeDelete > 0 {
		ids = append(ids, fields.fakeDelete)
	}
	ids = append(ids, fields.strings...)
	ids = append(ids, fields.uintegersNullable...)
	ids = append(ids, fields.integersNullable...)
	ids = append(ids, fields.stringsEnums...)
	ids = append(ids, fields.bytes...)
	ids = append(ids, fields.sliceStringsSets...)
	ids = append(ids, fields.booleansNullable...)
	ids = append(ids, fields.floatsNullable...)
	timesNullableStart := len(ids)
	ids = append(ids, fields.timesNullable...)
	ids = append(ids, fields.datesNullable...)
	timesNullableEnd := len(ids)
	ids = append(ids, fields.jsons...)
	for k, i := range ids {
		name := subFieldPrefix + fields.fields[i].Name
		columns = append(columns, name)
		if (k >= timesStart && k < timesEnd) || (k >= timesNullableStart && k < timesNullableEnd) {
			fieldsQuery += ",TO_SECONDS(`" + name + "`)"
		} else {
			fieldsQuery += ",`" + name + "`"
		}
	}
	for i, subFields := range fields.structsFields {
		field := fields.fields[fields.structs[i]]
		prefixName := subFieldPrefix
		if !field.Anonymous {
			prefixName += field.Name
		}
		subColumns, subQuery := subFields.buildColumnNames(prefixName)
		columns = append(columns, subColumns...)
		fieldsQuery += subQuery
	}
	return columns, fieldsQuery
}

var scanIntNullablePointer = func() interface{} {
	return &sql.NullInt64{}
}

var pointerUintNullableScan = func(val interface{}) interface{} {
	v := val.(*sql.NullInt64)
	if v.Valid {
		return uint64(v.Int64)
	}
	return nil
}

var pointerIntNullableScan = func(val interface{}) interface{} {
	v := val.(*sql.NullInt64)
	if v.Valid {
		return v.Int64
	}
	return nil
}

var scanStringNullablePointer = func() interface{} {
	return &sql.NullString{}
}

var pointerStringNullableScan = func(val interface{}) interface{} {
	v := val.(*sql.NullString)
	if v.Valid {
		return v.String
	}
	return nil
}

var scanBoolPointer = func() interface{} {
	v := false
	return &v
}

var pointerBoolScan = func(val interface{}) interface{} {
	return *val.(*bool)
}

var scanBoolNullablePointer = func() interface{} {
	return &sql.NullBool{}
}

var pointerBoolNullableScan = func(val interface{}) interface{} {
	v := val.(*sql.NullBool)
	if v.Valid {
		return v.Bool
	}
	return nil
}

var scanFloatNullablePointer = func() interface{} {
	return &sql.NullFloat64{}
}

var pointerFloatNullableScan = func(val interface{}) interface{} {
	v := val.(*sql.NullFloat64)
	if v.Valid {
		return v.Float64
	}
	return nil
}

var scanStringPointer = func() interface{} {
	v := ""
	return &v
}

var pointerStringScan = func(val interface{}) interface{} {
	return *val.(*string)
}
