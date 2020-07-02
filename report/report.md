# Report

## Project 1

核心是实现 `kv/storage/standalone_storage/standalone_storage.go` 
当中的 `StandAloneStorage` 结构。需要重点关注的应该是如下两个 `interface` :

``` go
type Storage interface {
	Start() error
	Stop() error
	Write(ctx *kvrpcpb.Context, batch []Modify) error
	Reader(ctx *kvrpcpb.Context) (StorageReader, error)
}

type StorageReader interface {
	// When the key doesn't exist, return nil for the value
	GetCF(cf string, key []byte) ([]byte, error)
	IterCF(cf string) engine_util.DBIterator
	Close()
}
```

查看代码可以看到除了 `StandAloneStorage` 还有 `RaftStorage` 和 `MemStorage` 
实现了 `Storage` 接口，`MemStorage` 并不以 `badgers` 为底层键值存储引擎，主要是用于测试，不过 `RaftStorage` 
的实现是可以参考的。

Project1还是比较容易的，需要注意的是：
- `StandAloneStorage` 在实现 `Write` 接口时调用 transaction 的 Set 方法的时候应当传入 `${cf}_${key}` 的形式，
因为读取的时候使用的 `GetCFFromTxn` 方法是将 `${cf}_${key}` 作为键的。

## Project 2

### Part A

