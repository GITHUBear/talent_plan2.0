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
