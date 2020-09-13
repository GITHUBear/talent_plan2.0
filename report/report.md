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

课程要求首先实现 `Leadership election`，课程中并没有给出实现的具体步骤，但是可以通过面向测试的方式进行逐步实现和完善。

### Part B

```
        +------------------+
        |  Peer +---------+|
        |       |   Peer  ||
        |       | Storage ||
        |       +---------+|
        |             ^    |
        |             |    |
        |     persist |    |
        |             |    |
        |       +---------+|
        |       |   Raw   ||
        |       |   Node  ||
        |       +---------+|
        +------------------+
```

一个 region 的各个 Peer 分布在多个 store 上

Peer 创建流程：

- peer.go: createPeer  检查指定 store_id 的 store 上是否保存有指定 region 
的 Peer
- peer.go: NewPeer 
- peer_storage.go: NewPeerStorage  从 raftdb 当中初始化 raftlocalstate 
以及从 kvdb 中初始化 raftapplystate, 并保存到了 peer_storage 结构的成员当中缓存, 
初始状态将初始化为5：

```go
raftState.LastIndex = 5
raftState.LastTerm = 5
raftState.HardState.Term = 5
raftState.HardState.Commit = 5
```

- rawnode.go: NewRawNode  此时 peer_storage 随着 config 一并传入了 NewRawNode 
并最后成为了 raftLog 的 storage 成员，在 raft.go 中的 Raft 结构中又通过 raft.RaftLog.storage.InitialState() 
对 vote、 commit index、 term 进行了初始化, 最初的情况 raftLog 部分成员设定为：

```
offset(first): 6  stabled(last): 5  ents: []
```

Raft Node 驱动流程：

```
                          +----------------+
    +--------------+      |                |
    |  RaftStorage |----->|    RaftStore   |
    +--------------+      |                |
                          +----------------+
                                   |
                                   | startWorkers
                                   V
                           +--------------+
                           |   RaftWorker |
                           +--------------+
                                   |
                                   | newPeerMsgHandler
                                   V
                          +-----------------+
                          |  peerMsgHandler |
                          |     +---------+ |
                          |     |   Peer  | |
                          |     +---------+ |
                          +-----------------+
```

#### 遇到的问题

i. 在本阶段未添加任何代码的情况下运行 `make project2b`, 会得到 appendEntries 
位置的 segmentation violation:

```
github.com/pingcap-incubator/tinykv/raft.(*Raft).appendEntries(0xc0000f86e0, 0xc00013b590, 0x1, 0x1)
        /Users/dimdew/go/src/tinykv/raft/raft.go:670 +0x26a
```

最后发现是 config.peers 的大小是 0, 导致 newRaft 的时候没有正确初始化 Prs map

解决方法：

在上面的 Peer 创建流程分析中的最后一步中，在创建 Raft 的时候使用了 raft.RaftLog.storage.InitialState() 
而这个方法调用实际返回了两个返回值 HardState 在之前的实现当中就已经用于恢复 vote、 commit index、 term，
而之前尚未使用的 ConfState 中的 Nodes 成员应该就保存了 raft group 当中的其他成员 ID, 然后相应修改 newRaft 即可

ii. 