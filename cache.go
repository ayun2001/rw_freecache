package freecache

import (
	"encoding/binary"
	"sync/atomic"
	"github.com/cespare/xxhash"
	"time"
	"math"
)

type CacheStatus struct {
	TimeStamp          int64 `json:"time_stamp"`
	TimeSlice          int64 `json:"time_slice"`
	ItemsCount         int64 `json:"items_count"`
	hitCount           int64 `json:"-"`
	lookupCount        int64 `json:"-"`
	HitRate            float64 `json:"hit_rate"`
	AvgAccessTime      float64 `json:"avg_access_time"`
	AvgLookupPerSecond float64 `json:"avg_lookup_per_second"`
	AvgHitPerSecond    float64 `json:"avg_hit_per_second"`
}

func getCurrentTimestamp() int64 {
	return int64(time.Now().Unix())
}

//fix float64 length
func Float64ToFixed(f float64, places int) float64 {
	shift := math.Pow(10, float64(places))
	fv := 0.0000000001 + f //对浮点数产生.xxx999999999 计算不准进行处理
	return math.Floor(fv * shift + .5) / shift
}

type Cache struct {
	segments   [256]segment
	hitCount   int64
	missCount  int64
	lastStatus CacheStatus
}

func hashFunc(data []byte) uint64 {
	return xxhash.Sum64(data)
}

// The cache size will be set to 512KB at minimum.
// If the size is set relatively large, you should call
// `debug.SetGCPercent()`, set it to a much smaller value
// to limit the memory consumption and GC pause time.
func NewCache(size int) (cache *Cache) {
	if size < 512 * 1024 {
		size = 512 * 1024
	}
	cache = new(Cache)
	for i := 0; i < 256; i++ {
		cache.segments[i] = newSegment(size / 256, i)
	}
	cache.lastStatus = CacheStatus{TimeStamp: getCurrentTimestamp()}
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
	if cache.hitCount >= MAX_INT64_NUM || cache.missCount >= MAX_INT64_NUM {
		cache.ResetStatistics() //reset all status count, two
	}
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
		return Float64ToFixed(float64(cache.HitCount()) / float64(lookupCount), 3)
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
	cache.lastStatus.TimeStamp = getCurrentTimestamp()
	cache.lastStatus.TimeSlice = 0
	cache.lastStatus.ItemsCount = 0
	cache.lastStatus.hitCount = 0
	cache.lastStatus.lookupCount = 0
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
	cache.lastStatus.TimeStamp = getCurrentTimestamp()
	cache.lastStatus.TimeSlice = 0
	cache.lastStatus.ItemsCount = 0
	cache.lastStatus.hitCount = 0
	cache.lastStatus.lookupCount = 0
	cache.lastStatus.HitRate = 0
}

func (cache *Cache) GetStatistics() *CacheStatus {
	now := getCurrentTimestamp()
	currentStatus := CacheStatus{TimeStamp: now, TimeSlice: now - cache.lastStatus.TimeStamp}
	itemsCount := cache.EntryCount()
	hitCount := cache.HitCount()
	lookupCount := cache.LookupCount()
	currentStatus.ItemsCount = itemsCount
	if currentStatus.TimeSlice > 0 {
		currentStatus.hitCount = hitCount - cache.lastStatus.hitCount
		currentStatus.lookupCount = lookupCount - cache.lastStatus.lookupCount
		currentStatus.AvgLookupPerSecond = Float64ToFixed(float64(currentStatus.lookupCount) / float64(currentStatus.TimeSlice), 3)
		currentStatus.AvgHitPerSecond = Float64ToFixed(float64(currentStatus.hitCount) / float64(currentStatus.TimeSlice), 3)
		if currentStatus.lookupCount > 0 {
			currentStatus.HitRate = Float64ToFixed(float64(currentStatus.hitCount) / float64(currentStatus.lookupCount), 3)
			currentStatus.AvgAccessTime = Float64ToFixed((float64(currentStatus.TimeSlice) / float64(currentStatus.lookupCount)) * 1000, 3)  //Microsecond
		} else {
			currentStatus.HitRate = 0
			currentStatus.AvgAccessTime = 0
		}
		cache.lastStatus.TimeStamp = now
		cache.lastStatus.TimeSlice = 0
		cache.lastStatus.ItemsCount = itemsCount
		cache.lastStatus.hitCount = hitCount
		cache.lastStatus.lookupCount = lookupCount
		cache.lastStatus.HitRate = 0

	}
	return &currentStatus
}
