package simple_metrics

import (
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/latolukasz/beeorm/v2"
)

const PluginCode = "github.com/latolukasz/beeorm/plugins/simple_metrics"

type Plugin struct {
	options         *Options
	mySQLLogHandler *mySQLLogHandler
}
type Options struct {
	mySQLMetricsLimits    int
	mySQLSlowQueriesLimit int
}

func InitOptions() *Options {
	return &Options{}
}

func (o *Options) EnableMySQLMetrics(metricsLimits int) *Options {
	o.mySQLMetricsLimits = metricsLimits
	return o
}

func (o *Options) EnableMySQLSlowQuery(maxQueries int) *Options {
	o.mySQLSlowQueriesLimit = maxQueries
	return o
}

type MySQLQuery struct {
	Counter     uint64
	SlowQueries uint64
	TotalTime   uint64
	Table       string
	Pool        string
	Operation   MySQLQueryType
}

type mySQLQuery struct {
	Counter     uint64
	Time        uint64
	SlowQueries uint64
}

type MySQLSLowQuery struct {
	Query    string
	Pool     string
	Duration time.Duration
}

func (sq *MySQLSLowQuery) String() string {
	return "[" + string(sq.Pool) + "][" + sq.Duration.String() + "] " + sq.Query
}

type MySQLQueryType uint8

const (
	Query MySQLQueryType = iota
	Insert
	Update
	Delete
	Show
	Alter
	Other
)

type poolName string
type tableName string
type lazyQuery bool
type mySQLTableLazyGroup map[lazyQuery]*mySQLQuery
type mySQLTableGroup map[tableName]mySQLTableLazyGroup
type mySQLFlushTypeGroup map[MySQLQueryType]mySQLTableGroup
type mySQLQueriesStats map[poolName]mySQLFlushTypeGroup

type mySQLLogHandler struct {
	p                  *Plugin
	m                  sync.Mutex
	queries            mySQLQueriesStats
	slowQueries        *mySqlSlowQueryTreeNode
	slowQueriesCounter int
	mySQLMetricsLimits int
}

func Init(options *Options) *Plugin {
	if options == nil {
		options = &Options{}
	}
	plugin := &Plugin{options: options}
	if options.mySQLMetricsLimits > 0 || options.mySQLSlowQueriesLimit > 0 {
		plugin.mySQLLogHandler = &mySQLLogHandler{
			p:                  plugin,
			queries:            mySQLQueriesStats{},
			mySQLMetricsLimits: options.mySQLMetricsLimits,
		}
	}
	return plugin
}

func (p *Plugin) GetCode() string {
	return PluginCode
}

func (ml *mySQLLogHandler) Handle(log map[string]interface{}) {
	t := log["microseconds"].(int64)
	query := strings.ToLower(log["query"].(string))
	pool := poolName(log["pool"].(string))
	lazy := lazyQuery(false)
	meta, hasMeta := log["meta"]
	slow := false
	if hasMeta {
		metaData, isMetaData := meta.(beeorm.Bind)
		if isMetaData && metaData["lazy"] == "1" {
			lazy = true
		}
	}
	if !lazy && ml.p.options.mySQLSlowQueriesLimit > 0 {
		if ml.slowQueriesCounter < ml.p.options.mySQLSlowQueriesLimit {
			node := ml.slowQueries.insert(&MySQLSLowQuery{
				Query:    query,
				Pool:     string(pool),
				Duration: time.Microsecond * time.Duration(t),
			})
			if ml.slowQueries == nil {
				ml.slowQueries = node
			}
			ml.slowQueriesCounter++
		} else if ml.slowQueries != nil {
			min, parent := ml.slowQueries.findMin(nil)
			if min.value.Duration.Microseconds() <= t {
				if parent == nil {
					ml.slowQueries = ml.slowQueries.right
				} else {
					parent.left = min.right
				}
				ml.slowQueries.insert(&MySQLSLowQuery{
					Query:    query,
					Pool:     string(pool),
					Duration: time.Microsecond * time.Duration(t),
				})
				slow = true
			}
		}
	}

	if ml.mySQLMetricsLimits <= 0 {
		return
	}
	operation := log["operation"].(string)
	splitQuery := strings.Split(query, " ")
	table := tableName("unknown")
	queryType := Other
	switch operation {
	case "SELECT":
		switch splitQuery[0] {
		case "select":
			queryType = Query
			for k, part := range splitQuery[2:] {
				if part == "from" {
					table = ml.clearTableName(splitQuery[k+3])
					break
				}
			}
			break
		case "show":
			queryType = Show
			if splitQuery[1] == "tables" && splitQuery[2] == "like" {
				table = ml.clearTableName(splitQuery[3])
			} else if splitQuery[1] == "create" && splitQuery[2] == "table" {
				table = ml.clearTableName(splitQuery[3])
			} else if splitQuery[1] == "indexes" && splitQuery[2] == "from" {
				table = ml.clearTableName(splitQuery[3])
			}
			break
		}
		break
	case "EXEC":
		switch splitQuery[0] {
		case "update":
			queryType = Update
			table = ml.clearTableName(splitQuery[1])
			break
		case "insert":
			queryType = Insert
			table = ml.clearTableName(splitQuery[2])
			break
		case "delete":
			queryType = Delete
			table = ml.clearTableName(splitQuery[2])
			break
		case "alter":
			queryType = Alter
			table = ml.clearTableName(splitQuery[2])
			break
		case "set":
			queryType = Alter
			break
		}
		break
	}
	ml.m.Lock()
	defer ml.m.Unlock()

	l1 := ml.queries[pool]
	if l1 == nil {
		l1 = mySQLFlushTypeGroup{}
		ml.queries[pool] = l1
	}
	l2 := l1[queryType]
	if l2 == nil {
		l2 = mySQLTableGroup{}
		l1[queryType] = l2
	}
	l3 := l2[table]
	if l3 == nil {
		l3 = mySQLTableLazyGroup{}
		l2[table] = l3
	}
	l4 := l3[lazy]
	if l4 == nil {
		l4 = &mySQLQuery{}
		l3[lazy] = l4
		ml.mySQLMetricsLimits--
	}
	l4.Counter++
	l4.Time += uint64(t)
	if slow {
		l4.SlowQueries++
	}
}

func (ml *mySQLLogHandler) clearTableName(table string) tableName {
	s := strings.Split(table, "(")
	if len(s) > 1 {
		table = s[0]
	}
	s = strings.Split(table, ".")
	name := s[0]
	if len(s) > 1 {
		name = s[1]
	}
	return tableName(strings.Trim(name, "`'"))
}

func (p *Plugin) GetMySQLQueriesStats(l bool) []MySQLQuery {
	if p.mySQLLogHandler == nil {
		return nil
	}
	results := make([]MySQLQuery, 0)
	for pool, l1 := range p.mySQLLogHandler.queries {
		for operation, l2 := range l1 {
			for table, l3 := range l2 {
				q, has := l3[lazyQuery(l)]
				if has {
					query := MySQLQuery{
						Counter:     q.Counter,
						TotalTime:   q.Time,
						Pool:        string(pool),
						Table:       string(table),
						Operation:   operation,
						SlowQueries: q.SlowQueries,
					}
					results = append(results, query)
				}
			}
		}
	}
	sort.SliceStable(results, func(l, r int) bool {
		return results[l].TotalTime > results[r].TotalTime
	})
	return results
}

func (p *Plugin) GetMySQLSlowQueriesStats() []*MySQLSLowQuery {
	if p.mySQLLogHandler == nil {
		return nil
	}
	return p.mySQLLogHandler.slowQueries.getChildren()
}

func (p *Plugin) ClearMySQLStats() {
	if p.mySQLLogHandler != nil {
		p.mySQLLogHandler.m.Lock()
		defer p.mySQLLogHandler.m.Unlock()
		p.mySQLLogHandler.queries = mySQLQueriesStats{}
		p.mySQLLogHandler.mySQLMetricsLimits = p.options.mySQLMetricsLimits
	}
}

func (p *Plugin) ClearMySQLSlowQueries() {
	if p.mySQLLogHandler != nil {
		p.mySQLLogHandler.m.Lock()
		defer p.mySQLLogHandler.m.Unlock()
		p.mySQLLogHandler.slowQueries = nil
		p.mySQLLogHandler.slowQueriesCounter = 0
	}
}

func (p *Plugin) PluginInterfaceEngineCreated(engine beeorm.Engine) {
	if p.mySQLLogHandler != nil {
		engine.RegisterQueryLogger(p.mySQLLogHandler, true, false, false)
	}
}

type mySqlSlowQueryTreeNode struct {
	value *MySQLSLowQuery
	left  *mySqlSlowQueryTreeNode
	right *mySqlSlowQueryTreeNode
}

func (n *mySqlSlowQueryTreeNode) insert(value *MySQLSLowQuery) *mySqlSlowQueryTreeNode {
	if n == nil {
		return &mySqlSlowQueryTreeNode{value: value}
	}
	if value.Duration < n.value.Duration {
		n.left = n.left.insert(value)
	} else {
		n.right = n.right.insert(value)
	}
	return n
}

func (n *mySqlSlowQueryTreeNode) findMin(p *mySqlSlowQueryTreeNode) (min, parent *mySqlSlowQueryTreeNode) {
	if n == nil {
		return n, nil
	}
	if n.left != nil {
		return n.left.findMin(n)
	}
	return n, p
}

func (n *mySqlSlowQueryTreeNode) getChildren() []*MySQLSLowQuery {
	if n == nil {
		return nil
	}
	res := make([]*MySQLSLowQuery, 0)
	r := n.right.getChildren()
	if r != nil {
		res = append(res, n.right.getChildren()...)
	}
	res = append(res, n.value)
	l := n.left.getChildren()
	if l != nil {
		res = append(res, l...)
	}
	return res
}