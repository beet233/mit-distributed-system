# lab4-shardedkv

## Part 4A - The Shard Controller

一方面来说，当数据量过大时，单个 Server 的硬盘可能顶不住；另一方面，把数据分到多个 Server 上，可以实现并发的操作而不是全都由一个 Server 解决，提高了性能。为了保证高可用，我们给每个数据分片都用一个 Raft 集群。为了管理分片/协调各个分片集群，我们还需要一个协调者，The Shard Controller，它也采用 Raft 集群保证高可用。

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

#### Command Apply && Duplicated Request Process

因为 shard controller 也是基于 raft 的 service，这里和 lab3 中的处理方法是一模一样的。每个 client 有一个 clientId，并维护着递增的 requestId。server 端则将所有的请求都 Start 进入 raft 来达成共识，并运行一个 applyLoop 来保持接收 applyCh 传来的 applyMsg，根据其中的 clientId 和 requestId，更新 latestAppliedRequest（clientId -> latest applied requestId）或监测是否是重发的请求。

对于写请求（JOIN、LEAVE、MOVE），若检测到重复，则直接返回 OK 而不重复执行，对于读请求（QUERY），直接正常处理，无论是否重复。

至于代码实现上的 applyLoop 通过 waitCh 通知 RPC Handler 等细节，都与 lab3 相同。

#### Rebalance Shards Between Groups

这是 shard controller 的核心功能——如何选择一个策略，让各个分片均匀地分布在 groups 分片集群之中，平衡他们的压力。在增添/下线一些 groups 时尤其如此。

我这里的策略比较浅显易懂：

+ 当有一些新的空的 groups 需要获得分片时，进行以下循环：
  + 找到当前最多分片的 group 和最少分片的 group。
  + 若最多分片的 group 和最少分片的 group 的分片数量差小于等于 1，则已经平衡，结束。否则，把最多分片的 group 的片分给最少分片的 group 一个。继续上一步。
+ 当有一些孤儿分片没有归属的 groups 时，进行以下循环：
  + 找到当前最少分片的 group。
  + 把孤儿分片分给他一片。继续上一步，直到没有孤儿分片。

注意到，当系统最初还没有任何 group 时 JOIN，同时符合两种情况的描述，这时选择第二种是正确的。

另外，当最后一个 group 也被下线时，代码中需要把所有 shards 对应的 group 置为 0，是一个 Invalid Group 标记。

#### Iteration Of Map

这是一个实现上的小细节，因为每个 shard controller 的 raft replica 是各自进行 rebalance 分片的，我们需要保证他们会得到一样的结果。

然而，我一开始在 rebalance 方法里写了个 map 的 foreach 遍历，这在不同 server 完全可能导致不同的分片结果，因为 map 的遍历顺序是比较随机的。发现这个问题后，我把 map 中所有 key 取出来排序，搞了个有序的数组，然后通过遍历数组中有序的 key 来遍历 map 取 value，比较简单地给 map 的遍历打了个有序补丁（x。

## Part 4B - Sharded Key/Value Server

### Main Idea

