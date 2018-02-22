# rw_freecache

感谢 freecache 原作者对代码的无私贡献和其他开发者对代码的改善付出的卓越的努力。 

# 设计初心： 
freecache 因为其高效的性能获得很多人的喜爱，我也不例外。却因为里面使用了go Mutex导致了在非常高的并发情况下性能无法进一步提高，主要原因就是使用 get 方法中的Mutex.Lock()。 通过跟原作者的沟通后，发现因为 freecache get方法并不是只读方法，所以才动了念头修改一版能够满足自己高并发http平台的cache。

# 特性 （相比原 freecache）：
1. 将Mutex 修改成 RWMutex  --> 并发性能极大的提高
2. 去掉了segment访问时间计数器，重新添加平局访问计数器。（在 GetSummaryStatus 方法的返回值中）

# 性能测试 （performance）CPU:i7 6650U ：
    BenchmarkMapSet-4                           	 2000000	       916 ns/op
    BenchmarkCacheSet-4                         	 3000000	       516 ns/op
    BenchmarkCacheSetParallel-4                 	 5000000	       344 ns/op
    BenchmarkMapGet-4                           	10000000	       249 ns/op
    BenchmarkCacheGet-4                         	 5000000	       499 ns/op
    BenchmarkCacheGetParallel-4                 	20000000	       140 ns/op
    BenchmarkCacheGetWithExpiration-4           	 5000000	       518 ns/op
    BenchmarkCacheGetWithExpirationParallel-4   	20000000	       194 ns/op
    BenchmarkHashFunc-4                         	200000000	       7.77 ns/op

# 使用例子： （和原 freecache 没有区别）

```go
cacheSize := 100 * 1024 * 1024  // 100M
cache := freecache.NewCache(cacheSize)
debug.SetGCPercent(20)

key := []byte("abc")
val := []byte("def")
expire := 60 // expire in 60 seconds

cache.Set(key, val, expire)

got, err := cache.Get(key)
if err != nil {
    fmt.Println(err)
} else {
    fmt.Println(string(got))
}

affected := cache.Del(key)

fmt.Println("deleted key ", affected)
fmt.Println("entry count ", cache.EntryCount())
```
