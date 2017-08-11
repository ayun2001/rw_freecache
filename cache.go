package freecache

import (
	"encoding/binary"
	"sync/atomic"
	"github.com/spaolacci/murmur3"
	"time"
)

type CacheStatus struct {
	TimeStamp,
	TimeRange,
	ItemsCount,
	ExpiredCount,
	HitCount,
	LookupCount int64
	HitRate,
	AvgLookupPerSecond,
	AvgHitPerSecond float64
}

func getCurrTimestamp() int64 {
	return int64(time.Now().Unix())
}

type Cache struct {
	segments   [256]segment
	hitCount   int64
	missCount  int64
	lastStatus CacheStatus
}

func hashFunc(data []byte) uint64 {
	return murmur3.Sum64(data)
}

// The cache size will be set to 512KB at minimum.
// If the size is set relatively large, you should call
// `debug.SetGCPercent()`, set it to a much smaller value
// to limit the memory consumption and GC pause time.
func NewCache(size int) (cache *Cache) {
	if size < 512*1024 {
		size = 512 * 1024
	}
	cache = new(Cache)
	for i := 0; i < 256; i++ {
		cache.segments[i] = newSegment(size/256, i)
	}
	cache.lastStatus = CacheStatus{TimeStamp: getCurrTimestamp()}
	return
}

// If the key is larger than 65535 or value is larger than 1/1024 of the cache size,
// the entry will not be written to the cache. expireSeconds <= 0 means no expire,
// but it can be evicted when cache is full.
func (cache *Cache) Set(key, value []byte, expireSeconds int) (err error) {
	hashVal := hashFunc(key)
	segId := hashVal & 255
	err = cache.segments[segId].set(key, value, hashVal, expireSeconds)
	return
}

// Get the value or not found error.
func (cache *Cache) Get(key []byte) (value []byte, err error) {
	hashVal := hashFunc(key)
	segId := hashVal & 255
	value, err = cache.segments[segId].get(key, hashVal)
	if err == nil {
		atomic.AddInt64(&cache.hitCount, 1)
	} else {
		atomic.AddInt64(&cache.missCount, 1)
	}
	return
}

func (cache *Cache) TTL(key []byte) (timeLeft uint32, err error) {
	hashVal := hashFunc(key)
	segId := hashVal & 255
	timeLeft, err = cache.segments[segId].ttl(key, hashVal)
	return
}

func (cache *Cache) Del(key []byte) (affected bool) {
	hashVal := hashFunc(key)
	segId := hashVal & 255
	affected = cache.segments[segId].del(key, hashVal)
	return
}

func (cache *Cache) SetInt(key int64, value []byte, expireSeconds int) (err error) {
	var bKey [8]byte
	binary.LittleEndian.PutUint64(bKey[:], uint64(key))
	return cache.Set(bKey[:], value, expireSeconds)
}

func (cache *Cache) GetInt(key int64) (value []byte, err error) {
	var bKey [8]byte
	binary.LittleEndian.PutUint64(bKey[:], uint64(key))
	return cache.Get(bKey[:])
}

func (cache *Cache) DelInt(key int64) (affected bool) {
	var bKey [8]byte
	binary.LittleEndian.PutUint64(bKey[:], uint64(key))
	return cache.Del(bKey[:])
}

func (cache *Cache) EvacuateCount() (count int64) {
	for i := 0; i < 256; i++ {
		count += atomic.LoadInt64(&cache.segments[i].totalEvacuate)
	}
	return
}

func (cache *Cache) ExpiredCount() (count int64) {
	for i := 0; i < 256; i++ {
		count += atomic.LoadInt64(&cache.segments[i].totalExpired)
	}
	return
}

func (cache *Cache) EntryCount() (entryCount int64) {
	for i := 0; i < 256; i++ {
		entryCount += atomic.LoadInt64(&cache.segments[i].entryCount)
	}
	return
}

func (cache *Cache) HitCount() int64 {
	return atomic.LoadInt64(&cache.hitCount)
}

func (cache *Cache) LookupCount() int64 {
	return atomic.LoadInt64(&cache.hitCount) + atomic.LoadInt64(&cache.missCount)
}

func (cache *Cache) HitRate() float64 {
	lookupCount := cache.LookupCount()
	if lookupCount == 0 {
		return 0
	} else {
		return float64(cache.HitCount()) / float64(lookupCount)
	}
}

func (cache *Cache) OverwriteCount() (overwriteCount int64) {
	for i := 0; i < 256; i++ {
		overwriteCount += atomic.LoadInt64(&cache.segments[i].overwrites)
	}
	return
}

func (cache *Cache) Clear() {
	for i := 0; i < 256; i++ {
		seg := cache.segments[i]
		seg.lock.Lock()
		cache.segments[i] = newSegment(len(cache.segments[i].rb.data), i)
		seg.lock.Unlock()
	}
	atomic.StoreInt64(&cache.hitCount, 0)
	atomic.StoreInt64(&cache.missCount, 0)
	cache.lastStatus.TimeStamp = getCurrTimestamp()
	cache.lastStatus.TimeRange = 0
	cache.lastStatus.ExpiredCount = 0
	cache.lastStatus.ItemsCount = 0
	cache.lastStatus.HitCount = 0
	cache.lastStatus.LookupCount = 0
	cache.lastStatus.HitRate = 0
}

func (cache *Cache) ResetStatistics() {
	atomic.StoreInt64(&cache.hitCount, 0)
	atomic.StoreInt64(&cache.missCount, 0)
	for i := 0; i < 256; i++ {
		cache.segments[i].lock.Lock()
		cache.segments[i].resetStatistics()
		cache.segments[i].lock.Unlock()
	}
	cache.lastStatus.TimeStamp = getCurrTimestamp()
	cache.lastStatus.TimeRange = 0
	cache.lastStatus.ExpiredCount = 0
	cache.lastStatus.ItemsCount = 0
	cache.lastStatus.HitCount = 0
	cache.lastStatus.LookupCount = 0
	cache.lastStatus.HitRate = 0
}

func (cache *Cache) GetStatistics() *CacheStatus {
	now := getCurrTimestamp()
	currentStatus := CacheStatus{TimeStamp: now, TimeRange: now - cache.lastStatus.TimeStamp}
	itemsCount := cache.EntryCount()
	expiredCount := cache.ExpiredCount()
	hitCount := cache.HitCount()
	lookupCount := cache.LookupCount()
	if currentStatus.TimeRange > 0 {
		currentStatus.ExpiredCount = expiredCount
		currentStatus.ItemsCount = itemsCount
		currentStatus.HitCount = hitCount - cache.lastStatus.HitCount
		currentStatus.LookupCount = lookupCount - cache.lastStatus.LookupCount
		currentStatus.AvgLookupPerSecond = float64(currentStatus.LookupCount) / float64(currentStatus.TimeRange)
		currentStatus.AvgHitPerSecond = float64(currentStatus.HitCount) / float64(currentStatus.TimeRange)
		if currentStatus.LookupCount != 0 {
			currentStatus.HitRate = float64(currentStatus.HitCount) / float64(currentStatus.LookupCount)
		} else {
			currentStatus.HitRate = 0.0
		}
		cache.lastStatus.TimeStamp = now
		cache.lastStatus.TimeRange = 0
		cache.lastStatus.ExpiredCount = expiredCount
		cache.lastStatus.ItemsCount = itemsCount
		cache.lastStatus.HitCount = hitCount
		cache.lastStatus.LookupCount = lookupCount
		cache.lastStatus.HitRate = 0

	}
	return &currentStatus
}
