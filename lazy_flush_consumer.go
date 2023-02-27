package beeorm

import (
	"context"
	"time"

	"github.com/go-sql-driver/mysql"
)

const LazyFlushChannelName = "orm-lazy-flush-stream"
const LazyFlushGroupName = "orm-lazy-flush-consumer"

type LazyFlushConsumer struct {
	eventConsumerBase
	consumer                     *eventsConsumer
	lazyFlushQueryErrorResolvers []LazyFlushQueryErrorResolver
	flusher                      *flusher
}

func NewLazyFlushConsumer(engine Engine) *LazyFlushConsumer {
	c := &LazyFlushConsumer{}
	c.engine = engine.Clone().(*engineImplementation)
	c.engine.SetMetaData("lazy", "1")
	c.block = true
	c.blockTime = time.Second * 30
	c.flusher = &flusher{engine: engine.(*engineImplementation)}
	return c
}

type LazyFlushQueryErrorResolver func(engine Engine, event EventEntityFlushed, queryError *mysql.MySQLError) error

func (r *LazyFlushConsumer) RegisterLazyFlushQueryErrorResolver(resolver LazyFlushQueryErrorResolver) {
	r.lazyFlushQueryErrorResolvers = append(r.lazyFlushQueryErrorResolvers, resolver)
}

func (r *LazyFlushConsumer) Digest(ctx context.Context) bool {
	r.consumer = r.engine.GetEventBroker().Consumer(LazyFlushGroupName).(*eventsConsumer)
	r.consumer.eventConsumerBase = r.eventConsumerBase
	return r.consumer.Consume(ctx, 500, func(events []Event) {
		lazyEvents := make([]*entitySQLFlush, 0)
		for _, e := range events {
			var data []*entitySQLFlush
			e.Unserialize(&data)
			lazyEvents = append(lazyEvents, data...)
		}
		r.handleEvents(events, lazyEvents)
	})
}

func (r *LazyFlushConsumer) handleEvents(events []Event, lazyEvents []*entitySQLFlush) {
	defer func() {
		if rec := recover(); rec != nil {
			_, isMySQLError := rec.(*mysql.MySQLError)
			if !isMySQLError {
				panic(rec)
			}
			for i, e := range lazyEvents {
				f := &flusher{engine: r.engine}
				f.events = []*entitySQLFlush{e}
				func() {
					defer func() {
						if rec2 := recover(); rec2 != nil {
							mySQLError, stillMySQLError := rec.(*mysql.MySQLError)
							if !stillMySQLError {
								panic(rec2)
							}
							for _, errorResolver := range r.lazyFlushQueryErrorResolvers {
								if errorResolver(r.engine, e, mySQLError) == nil {
									events[i].Ack()
									return
								}
							}
							panic(rec2)
						}
						events[i].Ack()
					}()
					f.execute(false, true)
				}()
			}
		}
	}()
	f := &flusher{engine: r.engine}
	f.events = lazyEvents
	f.execute(false, true)
	f.flushCacheSetters()
}
