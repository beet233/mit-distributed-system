# lab4-shardedkv

## Part 4A - The Shard Controller

一方面来说，当数据量过大时，单个 Server 的硬盘可能顶不住；另一方面，把数据分到多个 Server 上，可以实现并发的操作而不是全都由一个 Server 解决，提高了性能。为了保证高可用，我们给每个数据分片都用一个 Raft 集群。为了管理分片/协调各个分片集群，我们还需要一个调度者，The Shard Controller，它也采用 Raft 集群保证高可用。

除了要支持新的分片集群加入/旧的分片集群离开以外，我们还要让数据的分片尽可能的均匀，平衡各个分片集群间的压力。

在 `shardkvctrler/server.go` 中，我们可以看到：

```go
type ShardCtrler struct {
	mu      sync.Mutex
	me      int
	rf      *raft.Raft
	applyCh chan raft.ApplyMsg

	// Your data here.

	configs []Config // indexed by config num
}
```

配置每次更新都直接以 ++index 作为版本号，加入到 configs 数组中。我们再看看 Config 里有什么：

```go
// The number of shards.
const NShards = 10

// A configuration -- an assignment of shards to groups.
// Please don't change this.
type Config struct {
	Num    int              // config number
	Shards [NShards]int     // shard -> gid
	Groups map[int][]string // gid -> servers[]
}
```

本 lab 把数据分成 10 片，Config 里记录了每片被放在哪个分片集群（Group），每个分片集群（Group）内的 raft servers 则保存在 Config.Groups 中，注意这里 servers 是个 `[]string`，比较奇怪，实际上保存的是每个 raft server 的 server id，我们会在后续的 shardkv 中看到，实际存储数据的 server 有一个 func，可以把 `string` 转换成 `*labrpc.ClientEnd`。

### Main Idea



## Part 4B - Sharded Key/Value Server

### Main Idea

