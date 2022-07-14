package beeorm

import (
	"io/ioutil"
	"log"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLocalCache(t *testing.T) {
	registry := &Registry{}
	registry.RegisterLocalCache(100)
	validatedRegistry, def, err := registry.Validate()
	assert.Nil(t, err)
	defer def()
	engine := validatedRegistry.CreateEngine()
	testLogger := &testLogHandler{}
	engine.RegisterQueryLogger(testLogger, false, false, true)
	testQueryLog := &defaultLogLogger{maxPoolLen: 0, logger: log.New(ioutil.Discard, "", 0)}
	engine.RegisterQueryLogger(testQueryLog, false, false, true)

	c := engine.GetLocalCache()
	assert.Equal(t, "default", c.GetPoolConfig().GetCode())
	assert.Equal(t, 100, c.GetPoolConfig().GetLimit())
	val := c.GetSet("test_get_set", 10, func() interface{} {
		return "hello"
	})
	assert.Equal(t, "hello", val)
	assert.Len(t, testLogger.Logs, 2)
	val = c.GetSet("test_get_set", 10, func() interface{} {
		return "hello2"
	})
	assert.Equal(t, "hello", val)
	assert.Len(t, testLogger.Logs, 3)

	val, has := c.Get("test_get")
	assert.False(t, has)
	assert.Nil(t, val)

	c.Set("test_get", "hello")
	val, has = c.Get("test_get")
	assert.True(t, has)
	assert.Equal(t, "hello", val)

	engine = validatedRegistry.CreateEngine()
	engine.RegisterQueryLogger(testLogger, false, false, true)
	engine.RegisterQueryLogger(testQueryLog, false, false, true)
	c = engine.GetLocalCache()
	val, has = c.Get("test_get")
	assert.True(t, has)
	assert.Equal(t, "hello", val)

	values := c.MGet("m_get_1", "m_get_2", "m_get_3")
	assert.Len(t, values, 3)
	assert.Nil(t, values[0])
	assert.Nil(t, values[1])
	assert.Nil(t, values[2])

	c.MSet("m_get_1", "a", "m_get_3", "c")
	values = c.MGet("m_get_1", "m_get_2", "m_get_3")
	assert.Len(t, values, 3)
	assert.Equal(t, "a", values[0])
	assert.Nil(t, values[1])
	assert.Equal(t, "c", values[2])

	c.Remove("m_get_1", "test_get_set")
	values = c.MGet("m_get_1", "test_get_set")
	assert.Len(t, values, 2)
	assert.Nil(t, values[0])
	assert.Nil(t, values[1])
}

func BenchmarkLocalCache(b *testing.B) {
	registry := &Registry{}
	registry.RegisterLocalCache(100)
	validatedRegistry, def, _ := registry.Validate()
	defer def()
	engine := validatedRegistry.CreateEngine()
	c := engine.GetLocalCache()
	c.Set("test", "Hello")
	b.ResetTimer()
	b.ReportAllocs()
	for n := 0; n < b.N; n++ {
		c.Get("test")
	}
}
