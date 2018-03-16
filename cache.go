package freecache

import (
	"time"
	"math"
	"unsafe"
	"reflect"
	"encoding/binary"
	"sync/atomic"
	"github.com/cespare/xxhash"
)

const (
	minBufSize = 512 * 1024
	maxSegmentItemCount = int64(9223372036854775800 / 256)
)

type CacheSummaryStatus struct {
	hit_count            int64 `json:"-"`
	lookup_count         int64 `json:"-"`
	expired_count        int64 `json:"-"`
	evacuate_count       int64 `json:"-"`
	TimeStamp            int64 `json:"time_stamp"`
	TimeSlice            int64 `json:"time_slice"`
	ItemsCount           int64 `json:"items_count"`
	HitRate              float64 `json:"hit_rate"`
	AvgAccessTime        float64 `json:"avg_access_time"`
	AvgLookupPerSecond   float64 `json:"avg_lookup_per_second"`
	AvgHitPerSecond      float64 `json:"avg_hit_per_second"`
	AvgExpiredPerSecond  float64 `json:"avg_expired_per_second"`
	AvgEvacuatePerSecond float64 `json:"avg_evacuate_per_second"`
}

//fix float64 length
func float64ToFixed(f float64, places int) float64 {
	shift := math.Pow(10, float64(places))
	fv := 0.0000000001 + f //对浮点数产生.xxx999999999 计算不准进行处理
	return math.Floor(fv * shift + .5) / shift
}

func stringToBytes(s string) []byte {
	return *(*[]byte)(unsafe.Pointer((*reflect.SliceHeader)(unsafe.Pointer(&s))))
}

func getCurrTimestamp() int64 {
	return int64(time.Now().Unix())
}

type Cache struct {
	segments   [256]segment
	lastStatus CacheSummaryStatus
}

func hashFunc(data []byte) uint64 {
	return xxhash.Sum64(data)
}

// The cache size will be set to 512KB at minimum.
// If the size is set relatively large, you should call
// `debug.SetGCPercent()`, set it to a much smaller value
// to limit the memory consumption and GC pause time.
func NewCache(size int) (cache *Cache) {
	if size < minBufSize {
		size = minBufSize
	}
	cache = new(Cache)
	for i := 0; i < 256; i++ {
		cache.segments[i] = newSegment(size / 256, i)
	}
	cache.lastStatus = CacheSummaryStatus{TimeStamp: getCurrTimestamp()}
	return
}

// ===============================================================

// If the key is larger than 65535 or value is larger than 1/1024 of the cache size,
// the entry will not be written to the cache. expireSeconds <= 0 means no expire,
// but it can be evicted when cache is full.
func (cache *Cache) Set(key, value []byte, expireSeconds int) (err error) {
	hashVal := hashFunc(key)
	segId := hashVal & 255
	if cache.segments[segId].totalCount >= maxSegmentItemCount ||
		cache.segments[segId].totalEvacuate >= maxSegmentItemCount {
		cache.segments[segId].clear()
	}
	err = cache.segments[segId].set(key, value, hashVal, expireSeconds)
	return
}

// Get the value or not found error.
func (cache *Cache) Get(key []byte) (value []byte, err error) {
	hashVal := hashFunc(key)
	segId := hashVal & 255
	if cache.segments[segId].totalExpired >= maxSegmentItemCount ||
		cache.segments[segId].missCount >= maxSegmentItemCount ||
		cache.segments[segId].hitCount >= maxSegmentItemCount {
		cache.segments[segId].clear()
	}
	value, _, err = cache.segments[segId].get(key, hashVal)
	return
}

// Get the value or not found error.
func (cache *Cache) GetWithExpiration(key []byte) (value []byte, expireAt uint32, err error) {
	hashVal := hashFunc(key)
	segId := hashVal & 255
	if cache.segments[segId].totalExpired >= maxSegmentItemCount ||
		cache.segments[segId].missCount >= maxSegmentItemCount ||
		cache.segments[segId].hitCount >= maxSegmentItemCount {
		cache.segments[segId].clear()
	}
	value, expireAt, err = cache.segments[segId].get(key, hashVal)
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

// ===============================================================

func (cache *Cache) SetStr(key string, value []byte, expireSeconds int) error {
	return cache.Set(stringToBytes(key), value, expireSeconds)
}

func (cache *Cache) SetStrEx(key, value string, expireSeconds int) error {
	return cache.Set(stringToBytes(key), stringToBytes(value), expireSeconds)
}

func (cache *Cache) GetStr(key string) ([]byte, error) {
	return cache.Get(stringToBytes(key))
}

func (cache *Cache) GetStrWithExpiration(key string) ([]byte, uint32, error) {
	return cache.GetWithExpiration(stringToBytes(key))
}

func (cache *Cache) TTLStr(key string) (uint32, error) {
	return cache.TTL(stringToBytes(key))
}

func (cache *Cache) DelStr(key string) bool {
	return cache.Del(stringToBytes(key))
}

// ===============================================================

func (cache *Cache) SetInt(key int64, value []byte, expireSeconds int) error {
	var bKey [8]byte
	binary.LittleEndian.PutUint64(bKey[:], uint64(key))
	return cache.Set(bKey[:], value, expireSeconds)
}

func (cache *Cache) GetInt(key int64) ([]byte, error) {
	var bKey [8]byte
	binary.LittleEndian.PutUint64(bKey[:], uint64(key))
	return cache.Get(bKey[:])
}

func (cache *Cache) GetIntWithExpiration(key int64) ([]byte, uint32, error) {
	var bKey [8]byte
	binary.LittleEndian.PutUint64(bKey[:], uint64(key))
	return cache.GetWithExpiration(bKey[:])
}

func (cache *Cache) DelInt(key int64) bool {
	var bKey [8]byte
	binary.LittleEndian.PutUint64(bKey[:], uint64(key))
	return cache.Del(bKey[:])
}

func (cache *Cache) TTLInt(key int64) (uint32, error) {
	var bKey [8]byte
	binary.LittleEndian.PutUint64(bKey[:], uint64(key))
	return cache.TTL(bKey[:])
}

// ===============================================================

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

func (cache *Cache) HitCount() (count int64) {
	for i := range cache.segments {
		count += atomic.LoadInt64(&cache.segments[i].hitCount)
	}
	return
}

func (cache *Cache) MissCount() (count int64) {
	for i := range cache.segments {
		count += atomic.LoadInt64(&cache.segments[i].missCount)
	}
	return
}

func (cache *Cache) LookupCount() int64 {
	return cache.HitCount() + cache.MissCount()
}

func (cache *Cache) HitRate() float64 {
	hitCount, missCount := cache.HitCount(), cache.MissCount()
	lookupCount := hitCount + missCount
	if lookupCount == 0 {
		return 0
	} else {
		return float64ToFixed(float64(hitCount) / float64(lookupCount), 3)
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
		cache.segments[i].clear()
	}
	cache.lastStatus.TimeStamp = getCurrTimestamp()
	cache.lastStatus.TimeSlice = 0
	cache.lastStatus.ItemsCount = 0
	cache.lastStatus.HitRate = 0
	cache.lastStatus.hit_count = 0
	cache.lastStatus.lookup_count = 0
	cache.lastStatus.evacuate_count = 0
	cache.lastStatus.expired_count = 0
}

func (cache *Cache) ResetStatistics() {
	for i := 0; i < 256; i++ {
		cache.segments[i].resetStatistics()
	}
	cache.lastStatus.TimeStamp = getCurrTimestamp()
	cache.lastStatus.TimeSlice = 0
	cache.lastStatus.ItemsCount = 0
	cache.lastStatus.HitRate = 0
	cache.lastStatus.hit_count = 0
	cache.lastStatus.lookup_count = 0
	cache.lastStatus.evacuate_count = 0
	cache.lastStatus.expired_count = 0
}

func (cache *Cache) GetSummaryStatus() CacheSummaryStatus {
	now := getCurrTimestamp()
	currentStatus := CacheSummaryStatus{TimeStamp: now, TimeSlice: now - cache.lastStatus.TimeStamp}
	itemsCount := cache.EntryCount()
	hit_count := cache.HitCount()
	lookup_count := cache.LookupCount()
	expired_count := cache.ExpiredCount()
	evacuate_count := cache.EvacuateCount()
	currentStatus.ItemsCount = itemsCount
	if currentStatus.TimeSlice > 0 {
		currentStatus.hit_count = hit_count - cache.lastStatus.hit_count
		currentStatus.lookup_count = lookup_count - cache.lastStatus.lookup_count
		currentStatus.evacuate_count = evacuate_count - cache.lastStatus.evacuate_count
		currentStatus.expired_count = expired_count - cache.lastStatus.expired_count
		currentStatus.AvgLookupPerSecond = float64ToFixed(float64(currentStatus.lookup_count) / float64(currentStatus.TimeSlice), 3)
		currentStatus.AvgHitPerSecond = float64ToFixed(float64(currentStatus.hit_count) / float64(currentStatus.TimeSlice), 3)
		currentStatus.AvgExpiredPerSecond = float64ToFixed(float64(currentStatus.expired_count) / float64(currentStatus.TimeSlice), 3)
		currentStatus.AvgEvacuatePerSecond = float64ToFixed(float64(currentStatus.evacuate_count) / float64(currentStatus.TimeSlice), 3)
		if currentStatus.lookup_count > 0 {
			currentStatus.HitRate = float64ToFixed(float64(currentStatus.hit_count) / float64(currentStatus.lookup_count), 3)
			currentStatus.AvgAccessTime = float64ToFixed((float64(currentStatus.TimeSlice) / float64(currentStatus.lookup_count)) * 1000, 3) //Microsecond
		} else {
			currentStatus.HitRate = 0
			currentStatus.AvgAccessTime = 0
		}
		cache.lastStatus.TimeStamp = now
		cache.lastStatus.hit_count = hit_count
		cache.lastStatus.lookup_count = lookup_count
		cache.lastStatus.expired_count = expired_count
		cache.lastStatus.evacuate_count = evacuate_count
	}
	return currentStatus
}
