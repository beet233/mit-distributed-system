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

在完成了 Shard Controller 之后，这部分的问题就在于如何去根据最新的配置来分配、转移数据片，并实现老配置到新配置的平稳升级。这里我们需要设计一些 Sharded KV Server Group 之间相互调用的 RPC 接口，完成数据片的转移过程。

### Main Idea

#### Which Group To Request

首先，我们怎么分片，怎么判断一个 key 是属于哪个片？这个 lab 里直接给好了，是依赖编码把字符转换成数字，然后 mod NShards 来判断所属的 shard。

```go
func key2shard(key string) int {
	shard := 0
	if len(key) > 0 {
		shard = int(key[0])
	}
	shard %= shardctrler.NShards
	return shard
}
```

然后客户端需要读写某些 key 时，就先判断它属于哪个 shard，然后再去配置里找这个 shard 当前属于哪个 group。

现在的客户端除了需要找到 group 中的 leader 以外，还有了一个问题：如果 config 不是最新的或者 server 那边还没更新到最新的 config 状态，那还会出现 ErrWrongGroup。我们在遇到 ErrWrongGroup 时，只需要等待一下，再找 shardctrler 拉取一下最新的 config 后，重新找 key 所属的 group 进行请求即可。

```go
func (ck *Clerk) Get(key string) string {
	ck.mu.Lock()
	defer ck.mu.Unlock()

	args := GetArgs{}
	args.Key = key
	args.ClientId = ck.clientId
	args.RequestId = ck.requestId

	for {
		shard := key2shard(key)
		gid := ck.config.Shards[shard]
		if servers, ok := ck.config.Groups[gid]; ok {
			// try each server for the shard.
			for si := 0; si < len(servers); si++ {
				srv := ck.make_end(servers[si])
				var reply GetReply
				ok := srv.Call("ShardKV.Get", &args, &reply)
				if ok && (reply.Err == OK || reply.Err == ErrNoKey) {
					ck.requestId += 1
					return reply.Value
				}
				if ok && (reply.Err == ErrWrongGroup) {
					break
				}
				// ... not ok, or ErrWrongLeader
			}
		}
		time.Sleep(100 * time.Millisecond)
		// ask controler for the latest configuration.
		ck.config = ck.sm.Query(-1)
	}

	return ""
}
```

#### Shard Transfer Between Groups

首先 server 这边需要搞一个线程来轮询 shardctrler 更新自己的 config。在获得了最新的 config 后，有些 shard 需要离开 group，有些 shard 需要从别的 group 迁移过来。为了保证这样双向过程的简洁，容易想到，每个 group 只负责主动发起获取 shard 的任务，而删除 shard 则是被动的，由其他 group 向自己请求而驱动。

然后再来考虑，我们各个 group 间的请求肯定不是让所有 raft replica 都来发，这样复杂且无法把控，必然是由 group 的 leader 来相互通信。我们可以在某个 group A 收到 group B 获取 shard 的请求后，就删除掉自己的 shard 吗？这里是不行的。既有可能 group B 的某个 server 拿到数据后，自己却不是 leader 了，导致数据无法分享；也有可能 group A 删除了 shard 并 return RPC 请求，结果返回发送过程中网络断了。任一情况都会导致一个 shard 的数据彻底丢失。

解决的办法就是，我们在把 shard 数据发给 group B 后，暂时并不从自己这里删除（当然，为了保证数据的一致，我们需要设计 shard 的状态，在迁移过程中不能轻易修改 shard 导致两边有不一样的结果，后续会讲），而由 group B 确认自己已经收到数据并保存完毕（raft 达成共识）后，再告诉 group A 可以删除，相当于一个删除确认机制。

再有，这个通知删除的 RPC 也完全可能失败，所以为了保证过程的清晰，我们要求，group B 也必须在 group A 删除成功后，再有机会进入下一个 config 版本，避免 group A 一直持有 shard。

这样一来，shard 的状态设计就很清楚了：

```go
type ShardStatus int

const (
   WORKING = 0 // 正常工作
   PULLING = 1 // 等待从之前的 group 获取
   LEAVING = 2 // 等待新的 group 确认获取
   WAITING = 3 // 等待之前的 group 删除
)
```

把 Shard 设计为一个结构体，保存状态并分 shard 保存 kv，方便迁移：

```go
type Shard struct {
	Storage map[string]string
	Status  ShardStatus
}
```

只有 WORKING 状态和 WAITING 状态允许读写 KV 存储。

我们规定，config 版本一次只能增长 1 而不能跳跃版本。并且，只有当所有 shard 状态均为 WORKING 时，才能进行 config 升级。这个检查确认可以在轮询 shardctrler 更新 config 的那个线程里做。

在 groups 间，我设计了 Pull 和 Leave 两个 RPC：

```go
// Pull -> 请求目标 group 将 shard 传给自己
// Leave -> 请求目标 group 删除 shard，表示确认已经转移到自己

type PullArgs struct {
   ConfigVersion int
   ShardId       int
}

type PullReply struct {
   Storage map[string]string
   Err     Err
}

type LeaveArgs struct {
   ConfigVersion int
   ShardId       int
}

type LeaveReply struct {
   Err Err
}
```

Pull 请求是一个对 LEAVING 状态下的 shard 的读取，这个状态下 KV 是不可写的，所以由 leader 直接返回即可，不需要用 raft 达成共识。

在请求的参数里我添加了 ConfigVersion，避免误回复来自不同版本的请求。

而 Leave 请求则不一样，因为 Leave RPC 一旦返回 OK，就意味着 group B 可能达成 group A 已经删除的共识，进入下一个 config 版本。此时如果 Leave RPC 在达成删除 shard 的共识前返回了 OK，就可能出现：group A 接收 RPC 的 server 出现了分区或者崩溃，导致 group A 实际大多数 server 还处于 LEAVING 状态，并没有删除 shard，group B 却以为 group A 已经完成，自己去下一个版本，没人再来让 group A 完成升级了。

所以，我们必须保证 Leave RPC 返回 OK 时，一定已经全 raft servers 达成删除 shard 的共识。这就像应对 client 的读写请求一样，要用 waitCh 来等待接收 raft 执行的结果，确认后再返回。

```go
func (kv *ShardKV) Leave(args *LeaveArgs, reply *LeaveReply) {
   kv.mu.Lock()
   // 用 raft 保证所有 replica 都达成删除共识
   raftIndex, _, isLeader := kv.rf.Start(Op{Type: LEAVE_SHARD, ShardId: args.ShardId, ConfigVersion: kv.currConfig.Num})
   if !isLeader {
      reply.Err = ErrWrongLeader
      kv.mu.Unlock()
   } else {
      waitCh, exist := kv.waitChs[raftIndex]
      if exist {
         log.Fatalf("shardkv | server %d try to get a existing waitCh\n", kv.me)
      }
      kv.waitChs[raftIndex] = make(chan WaitChResponse, 1)
      waitCh = kv.waitChs[raftIndex]
      kv.mu.Unlock()
      // 选择在 server 端来判断超时，如果超时了还没返回，共识失败，认为 ErrWrongLeader
      select {
      case <-time.After(time.Millisecond * RaftTimeOut):
         kv.DPrintf("server %d timeout when handling Leave\n", kv.me)
         reply.Err = ErrWrongLeader
      case response := <-waitCh:
         reply.Err = response.err
      }
      // 用完后把 waitCh 删掉
      kv.mu.Lock()
      delete(kv.waitChs, raftIndex)
      kv.mu.Unlock()
   }
}
```

#### Apply Command Details

因为 sharded kv 是一个 multi-raft 结构，每个 group 内的更新操作都需要全 group 内达成 raft 共识。根据上述的思路，我整理出操作的类型：

```go
const (
   GET    = 0
   PUT    = 1
   APPEND = 2
   // shard 相关 op
   UPDATE_CONFIG = 3 // WORKING -> others
   PULL_SHARD    = 4 // get data, PULLING -> WORKING
   LEAVE_SHARD   = 5 // remove data, LEAVING -> nil
   TRANSFER_DONE = 6 // confirm transfer done, WAITING -> WORKING
)

type OpType int

type Op struct {
   // Your definitions here.
   // Field names must start with capital letters,
   // otherwise RPC will break.
   Type OpType
   // for GET, PUT, APPEND
   Key       string
   Value     string
   ClientId  int64
   RequestId int
   // for UPDATE_CONFIG
   Config shardctrler.Config
   // for PULL_SHARD, LEAVE_SHARD, TRANSFER_DONE
   ShardId       int
   ConfigVersion int
   Storage       map[string]string
}
```

只有 client 的 GET、PUT、APPEND 请求需要用 clientId 和 requestId 来做重复处理，其他的操作都可以根据现在的 shard 状态和 config 版本号来判断是否重复。

+ **UPDATE_CONFIG：**

  leader 从 shardctrler 获取到下一个版本的 config 后，用 raft 达成共识，把 config 分享给 followers。server 更新 config 后，同时需要保存上一份 config，用来辅助判断 Leave 等请求该发给谁。并且，在更新 config 时为各个 shard 做上标记，是 PULLING 还是 LEAVING 还是继续 WORKING，如果是 PULLING，启动一个线程来做主动发起拉取请求的任务。这个任务看起来只需要 leader 做，但是我们要做好 leader 崩溃/更换的准备，所以每个 server 都启动 pull 的任务，当检查到自己是 leader 时就去拉取，不是 leader 则循环等待，直到 PULLING -> WORKING，说明已经达成了获取到数据的共识。

  如果是第一份 config，那么此时还没有任何 KV 数据，新 shard 直接 WORKING。

+ **PULL_SHARD：**

  leader 获取到了 shard 数据后，用 raft 共识分享给所有 followers。这里保存好数据后，把状态更新 PULLING -> WORKING，并发起一个线程做让 shard 的上一任主人删除 shard 的任务。和上面类似，要做好 leader 崩溃/更换的准备，所以每个 server 都启动 leave 的任务，当检查到自己是 leader 时就去通知 leave，不是 leader 则循环等待，直到 WAITING -> WORKING，说明 shard 的上一任主人已经删除成功，自己这边也已经完成了等待结束的共识。

+ **LEAVE_SHARD：**

  leader 收到了可以删除 shard 的通知，共识删除 shard。状态由 LEAVING 变成直接不存在。因为这个操作是有 RPC 请求在等待完成的，所以要给 waitCh 返回一个结果。如果检测到已经提前被删除/版本都已经升级了，那表示对方可能出了点毛病而重发/落后，直接返回 OK 就行。

+ **TRANSFER_DONE：**

  在确认 Leave 请求成功后，说明 shard 的上一任主人已经删除完毕，迁移任务完成，leader 发动共识让所有 server 的 shard 状态更新 WAITING -> WORKING。

#### Snapshot && Persistence

需要持久化进快照的数据，除了 Shards 以外，还有防止重复执行的 latestAppliedRequest，latestAppliedRaftIndex，以及上一版本和当前版本的 config 信息。

```go
func (kv *ShardKV) serialize() []byte {
   kv.DPrintf("server %d serialize to a snapshot\n", kv.me)
   kv.mu.Lock()
   defer kv.mu.Unlock()
   w := new(bytes.Buffer)
   e := labgob.NewEncoder(w)
   e.Encode(kv.latestAppliedRaftIndex)
   e.Encode(kv.latestAppliedRequest)
   e.Encode(kv.prevConfig)
   e.Encode(kv.currConfig)
   e.Encode(kv.shards)
   data := w.Bytes()
   return data
}
```

存储快照、削减 raft 日志的时机依然是每次 apply 后，检测 raft 的日志大小是否到达阈值。
