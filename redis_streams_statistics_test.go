package beeorm

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestRedisStreamsStatus(t *testing.T) {
	registry := &Registry{}
	registry.RegisterRedis("localhost:6382", "", 11)
	registry.RegisterMySQLPool("root:root@tcp(localhost:3311)/test")
	registry.RegisterRedisStream("test-stream", "default", []string{"test-group"})
	validatedRegistry, err := registry.Validate()
	assert.NoError(t, err)
	engine := validatedRegistry.CreateEngine()
	r := engine.GetRedis()
	r.FlushDB()

	stats := engine.GetEventBroker().GetStreamsStatistics()
	assert.Len(t, stats, 3)
	valid := false
	for _, stream := range stats {
		if stream.Stream == "test-stream" {
			assert.Equal(t, "test-stream", stream.Stream)
			assert.Equal(t, "default", stream.RedisPool)
			assert.Equal(t, uint64(0), stream.Len)
			assert.Len(t, stream.Groups, 0)
			valid = true
		}
	}
	assert.True(t, valid)

	r.XGroupCreateMkStream("test-stream", "test-group", "0")
	flusher := engine.GetEventBroker().NewFlusher()
	type testEvent struct {
		Name string
	}
	for i := 1; i <= 10001; i++ {
		flusher.Publish("test-stream", testEvent{"b"})
	}
	flusher.Flush()
	time.Sleep(time.Millisecond * 500)

	stats = engine.GetEventBroker().GetStreamsStatistics("test-stream")
	valid = false
	for _, stream := range stats {
		if stream.Stream == "test-stream" {
			assert.Equal(t, uint64(10001), stream.Len)
			assert.Len(t, stream.Groups, 1)
			assert.Equal(t, "test-group", stream.Groups[0].Group)
			assert.Equal(t, uint64(0), stream.Groups[0].Pending)
			assert.Len(t, stream.Groups[0].Consumers, 0)
			valid = true
		}
	}
	assert.True(t, valid)

	consumer := engine.GetEventBroker().Consumer("test-group")
	consumer.DisableBlockMode()
	consumer.Consume(context.Background(), 11000, func(events []Event) {
		engine.GetRedis().Get("hello")
		engine.GetRedis().Get("hello2")
		engine.GetMysql().Query("SELECT 1")
		time.Sleep(time.Millisecond * 100)
	})

	statsSingle := engine.GetEventBroker().GetStreamStatistics("test-stream")
	assert.Equal(t, uint64(10001), statsSingle.Len)
	assert.Len(t, statsSingle.Groups, 1)
	assert.Equal(t, "test-group", statsSingle.Groups[0].Group)
	assert.Equal(t, uint64(0), statsSingle.Groups[0].Pending)
	assert.Len(t, statsSingle.Groups[0].Consumers, 0)

	flusher.Publish("test-stream", testEvent{"a"})
	flusher.Flush()
	assert.Panics(t, func() {
		consumer.Consume(context.Background(), 10, func(events []Event) {
			panic("stop")
		})
	})
	time.Sleep(time.Millisecond * 10)
	stats = engine.GetEventBroker().GetStreamsStatistics()
	valid = false
	for _, stream := range stats {
		if stream.Stream == "test-stream" {
			assert.Equal(t, uint64(1), stream.Groups[0].Pending)
			valid = true
		}
	}
	assert.True(t, valid)
}
