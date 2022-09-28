# lab2-raft

## 名词解释

+ term: 任期

## 目标

> A service calls `Make(peers,me,…)` to create a Raft peer. The peers argument is an array of network identifiers of the Raft peers (including this one), for use with RPC. The `me` argument is the index of this peer in the peers array. `Start(command)` asks Raft to start the processing to append the command to the replicated log. `Start()` should return immediately, without waiting for the log appends to complete. The service expects your implementation to send an `ApplyMsg` for each newly committed log entry to the `applyCh` channel argument to `Make()`.
>
> `raft.go` contains example code that sends an RPC (`sendRequestVote()`) and that handles an incoming RPC (`RequestVote()`). Your Raft peers should exchange RPCs using the labrpc Go package (source in `src/labrpc`). The tester can tell `labrpc` to delay RPCs, re-order them, and discard them to simulate various network failures. While you can temporarily modify `labrpc`, make sure your Raft works with the original `labrpc`, since that's what we'll use to test and grade your lab. Your Raft instances must interact only with RPC; for example, they are not allowed to communicate using shared Go variables or files.

（中文）

> 某服务调用 `Make(peers,me,…)` 来创造一个 Raft peer，peers 参数是 Raft peers 们的网络标识符数组（包括 me），`me` 参数是 peers 中当前 peer 的 index。`Start(command)` 让 Raft 开始处理一个 command，并加进它的复制 log 中。而 `Start()` 应该立刻返回，毕竟没有 log 嘛。服务希望当一个 log 被 commit 了后，通过 `applyCh` 这个 channel 发一条 `ApplyMsg ` 给它，这个 `applyCh` 是在 `Make` 中注册进去的。

## 反复测试脚本

A graceful script for multiple times tests, watching if there are accidental failures.

[Debugging by Pretty Printing (josejg.com)](https://blog.josejg.com/debugging-pretty/)

```shell
#!/usr/bin/env bash

trap 'exit 1' INT

echo "Running test $1 for $2 iters"
for i in $(seq 1 $2); do
	# -n do not output the trailing newline
	# -e enable interpretation of backslash escapes
    echo -ne "\r$i / $2\r"
    LOG="$1_$i.txt"
    # Failed go test return nonzero exit codes
    go test -run $1 &> $LOG
    if [[ $? -eq 0 ]]; then
        rm $LOG
    else
        echo "Failed at iter $i, saving log at $LOG"
    fi
done
```

## Part 2A - leader election

### Task

> Implement Raft leader election and heartbeats (`AppendEntries` RPCs with no log entries). The goal for Part 2A is for a single leader to be elected, for the leader to remain the leader if there are no failures, and for a new leader to take over if the old leader fails or if packets to/from the old leader are lost. Run `go test -run 2A `to test your 2A code.

### Hints

> + Fill in the `RequestVoteArgs` and `RequestVoteReply` structs. Modify `Make()` to create a background goroutine that will kick off leader election periodically by sending out `RequestVote` RPCs when it hasn't heard from another peer for a while. This way a peer will learn who is the leader, if there is already a leader, or become the leader itself. Implement the `RequestVote()` RPC handler so that servers will vote for one another.
>
> + To implement heartbeats, define an `AppendEntries` RPC struct (though you may not need all the arguments yet), and have the leader send them out periodically. Write an `AppendEntries` RPC handler method that resets the election timeout so that other servers don't step forward as leaders when one has already been elected.

### 核心资料

[Raft (thesecretlivesofdata.com)](http://thesecretlivesofdata.com/raft/#election) leader election 可视化

![image-20220908133117408](https://beetpic.oss-cn-hangzhou.aliyuncs.com/img/image-20220908133117408.png)

### 我的思路

先说说对上面的 Figure 2 中一些操作的理解。对于任何 RPC 请求，都带上了 term 任期这个参数，对于任何 server，收到 term > rf.currentTerm 时，说明我们自己这边的 term 已经过去了，别的 server 开启了新的任期，所以直接更新到这个任期，并转换为 follower。

如果收到 AppendEntries，把自己的 election 计时器清零，如果一段时间没收到，触发 election timeout，成为 candidate 并投票给自己，增加任期，向所有其他服务器求票，如果在下一次 election timeout 或收到新 leader 的 AppendEntries 前集齐了超过半数的票，那么就成为新 leader，开始向其他 servers 发送心跳。（反之如果收到新 leader 的 AppendEntries，直接成为 follower）

注意上面，candidate 有可能在集完票前已经变成了 follower，所以成为 leader 的一个前置条件是该 server 还是 candidate，而不是 follower。

一个 leader 挂了，后来复活，怎么顺利加入呢？

挂了之后，别的 server 会增加任期，所以当复活后发送/收到新的 RPC 请求时，就会根据任期大小规则，更新自己的 rf.currentTerm 并转换为 follower 重新加入 raft 集群。

### 具体实现

```go
type Raft struct {
   mu        sync.Mutex          // Lock to protect shared access to this peer's state
   peers     []*labrpc.ClientEnd // RPC end points of all peers
   persister *Persister          // Object to hold this peer's persisted state
   me        int                 // this peer's index into peers[]
   dead      int32               // set by Kill()

   // Your data here (2A, 2B, 2C).
   // Look at the paper's Figure 2 for a description of what
   // state a Raft server must maintain.
   electionCurrentTime int
   electionDuration    int
   currentTerm         int
   votedFor            int

   // follower | candidate | leader
   status                    string
   voteSum                   int
   requestVoteReplyChannel   chan RequestVoteReply
   appendEntriesReplyChannel chan AppendEntriesReply
}
```

新增下面  8 个 Raft 中的 state，其中 electionCurrentTime 记录当前已经经过的 election 等待时间，electionDuration 记录随机生成的 election timeout 时间，currentTerm 记录当前 server 的任期，votedFor 记录当前任期内该 server 投票给了谁，status 记录当前 server 身份，voteSum 记录当前 server 获得的票数（针对 candidate status），后面两个 channel 用于异步地接收 RPC 请求的 reply。

对于 RPC 请求结果的异步处理，是因为遇到了这样的问题：

刚开始的写法是直接 for 循环顺序且同步地发出所有请求，一个请求收到结果才处理下一个请求，但当某个 server 挂了时，这个请求的时间会非常长，导致许多奇怪的问题（一下子跳了好几个 election timeout 才收到结果），于是改成用 go 发请求，请求收到结果后传入一个 channel，一个 server 启动时顺便也开一个死循环阻塞的 consumer 来接收 channel 传来的信息。

```go
func (rf *Raft) sendRequestVoteWithChannelReply(server int, args *RequestVoteArgs, reply *RequestVoteReply) {
   // 失败的就不传回 channel 了
   ok := rf.peers[server].Call("Raft.RequestVote", args, reply)
   if ok {
      rf.requestVoteReplyChannel <- *reply
   }
}

func (rf *Raft) requestVoteReplyConsumer() {
   for {
      reply := <-rf.requestVoteReplyChannel
      DPrintf("server %d get requestVoteReply\n", rf.me)
      if reply.Term > rf.currentTerm {
         rf.currentTerm = reply.Term
         rf.status = "follower"
         DPrintf("server %d become follower for term update\n", rf.me)
      }
      if reply.VoteGranted {
         DPrintf("server %d get vote from other server\n", rf.me)
         rf.voteSum++
      }
   }
}
```

比如这个投票的 RPC，这样确实票数统计就做好了，那么什么时候检查票数呢？于是就直接在 ticker 里检查了（每 1 ms），若在下一次 election timeout 前的某个时刻检查到了票数过半并且我还是 candidate，那么就成为 leader。

注意维护 votedFor，已经投过票了的不能再投。

### 偶发性 bug

#### 某 server 成为 candidate 后，更新了另一个 server 的 term，还没成为 leader 发出心跳，另一个 server 也成为了 candidate，任期又递增

我认为在这种情况下，第一个 term 应该空缺，第二个 term 的 server 成为 leader，而程序出现 bug 的原因在于：

```
2022/09/11 00:09:13                 server 2 get requestVoteReply
2022/09/11 00:09:13 server 0 now become candidate // server 0 自己开启了 term 2
2022/09/11 00:09:13                 server 2 get 2 votes, len(rf.peers)/2 = 1, rf.status = "candidate"
2022/09/11 00:09:13                 server 2 get vote from other server
2022/09/11 00:09:13         server 1 become follower for term update
2022/09/11 00:09:13         server 1 vote for server 0
2022/09/11 00:09:13 server 0 get requestVoteReply
2022/09/11 00:09:13                 server 2 become follower for term update
2022/09/11 00:09:13                 server 2 now become leader
```

其中，`server 2 get 2 votes, len(rf.peers)/2 = 1, rf.status = "candidate"` 和 `server 2 now become leader` 在代码里是连在一起打印的，但我们发现在这期间，server 2 因 server 0 的 rpc 被变成了 follower，所以这里直接跳跃变成 leader 是不合法的操作，需要上锁。

也许我们需要对判断和改变一起操作的 part 都进行一个上锁，并记着哪些改变前需要判断自己的状况。

#### 某 server 经历了奇怪的事，成为 leader 后挂了，之后复活。从 leader 变回 follower 后，没为它重新开启 ticker()

在变回 follower 的时候加个判断，如果是从 leader 变回来的就加个 ticker。

### TODOs

+ ![image-20220908232028390](https://beetpic.oss-cn-hangzhou.aliyuncs.com/img/image-20220908232028390.png)

+ fix race，consider atomic variables. （done）
  目前的加锁思路是对更新了 term 和 status 的操作整块上锁，让身份发生重大变化的地方一次性完成而不中途变更 term 和 status，也就是先优先保证正确性。
  另外，通过一些重点变量的 getter 和 setter 单独加锁，解决 race。
  [(15条消息) Golang同步：原子操作使用_巴途Adolph的博客-CSDN博客_golang原子操作](https://blog.csdn.net/liuxinmingcode/article/details/50095615#:~:text=GO语言提供的原子操作都是非入侵式的，由标准库sync%2Fatomic中的众多函数代表,类型包括int32%2Cint64%2Cuint32%2Cuint64%2Cuintptr%2Cunsafe.Pointer，共六个。)

## Part 2B - log

基本思想就是 client 给 leader 发了一条指令，leader 广播给所有 server，收到过半的 server 复制成功的回复时，leader 将这条 log commit，并回复给 client。

> 一旦创建该日志条目的 leader 将它复制到过半的节点上时（比如图 6 中的条目 7），该日志条目就会被提交。 同时，leader 日志中该日志条目之前的所有日志条目也都会被提交，包括由之前的其他 leader 创建的日志条目。5.4 节会讨论在 leader 变更之后应用该规则的一些细节，并证明这种提交的规则是安全的。leader 会追踪它所知道的要提交的最高索引，并将该索引包含在未来的 AppendEntries RPC 中（包括心跳），以便其他的节点可以发现这个索引。一旦一个 follower 知道了一个日志条目被提交了。它就会将该日志条目按日志顺序应用到自己的状态机中。
> ————————————————
> 版权声明：本文为CSDN博主「-Hedon」的原创文章，遵循CC 4.0 BY-SA版权协议，转载请附上原文出处链接及本声明。
> 原文链接：https://blog.csdn.net/Hedon954/article/details/119186225

Raft 会一直维护着以下的特性，这些特性也同时构成了图 3 中的日志匹配特性（Log Matching Property）：

+ 如果不同日志中的两个条目有着相同的索引和任期值，那么它们就存储着相同的命令
+ 如果不同日志中的两个条目有着相同的索引和任期值，那么他们之前的所有日志条目也都相同

第一条特性源于这样一个事实，在给定的一个任期值和给定的一个日志索引中，一个 leader 最多创建一个日志条目，而且日志条目永远不会改变它们在日志中的位置。

第二条特性是由 AppendEntries RPC 执行的一个简单的一致性检查所保证的。当 leader 发送一个 AppendEntries RPC 的时候，leader 会将前一个日志条目的索引位置 `PrevLogIndex` 和任期号 `PrevLogTerm` 包含在里面（紧邻最新的日志条目）。如果一个 follower 在它的日志中找不到包含相同索引位置和任期号的条目，那么它就会拒绝该新的日志条目。一致性检查就像一个归纳步骤：一开始空的日志状态肯定是满足日志匹配特性（Log Matching Property）的，然后一致性检查保证了日志扩展时的日志匹配特性。因此，当 AppendEntries RPC 返回成功时，leader 就知道 follower 的日志一定和自己相同（从第一个日志条目到最新条目）。



在 Raft 算法中，leader 通过强制 follower 复制 leader 日志来解决日志不一致的问题。也就是说，follower 中跟 leader 冲突的日志条目会被 leader 的日志条目所覆盖。5.4 节会证明通过增加一个限制，这种方式就可以保证安全性。

为了使 follower 的日志跟自己（leader）一致，leader 必须找到两者达成一致的最大的日志条目索引，删除 follower 日志中从那个索引之后的所有日志条目，并且将自己那个索引之后的所有日志条目发送给 follower。所有的这些操作都发生在 AppendEntries RPCs 的一致性检查的回复中。leader 维护着一个针对每一个 follower 的 nextIndex，这个 nextIndex 代表的就是 leader 要发送给 follower 的下一个日志条目的索引。当选出一个新的 leader 时，该 leader 将所有的 nextIndex 的值都初始化为自己最后一个日志条目的 index 加 1（图7 中的 11）。如果一个 follower 的日志跟 leader 的是不一致的，那么下一次的 AppendEntries RPC 的一致性检查就会失败。AppendEntries RPC 在被 follower 拒绝之后，leader 对 nextIndex 进行减 1，然后重试 AppendEntries RPC。最终 nextIndex 会在某个位置满足 leader 和 follower 在该位置及之前的日志是一致的，此时，AppendEntries RPC 就会成功，将 follower 跟 leader 冲突的日志条目全部删除然后追加 leader 中的日志条目（需要的话）。一旦 AppendEntries RPC 成功，follower 的日志就和 leader 的一致了，并且在该任期接下来的时间里都保持一致。

全部追加的具体实现如下：我们发现 AppendEntries 可以携带多个 log  entris，那从 nextIndex 到末尾切片传入即可。

![image-20220922224750384](https://beetpic.oss-cn-hangzhou.aliyuncs.com/img/image-20220922224750384.png)



Raft 通过无限重试来处理 RequestVote 和 AppendEntries 的失败，如果崩溃的节点重启了，那么这些 RPC 就会被成功地完成。Raft 的 RPCs 都是幂等的，所以重复发送相同的 RPCs 不会对系统造成危害。实际情况下，一个 follower 如果接收了一个 AppendEntries 请求，但是这个请求里面的这些日志条目在它日志中已经有了，它就会直接忽略这个新的请求中的这些日志条目。



这里有个疑问：如果某节点挂了很久，无限重试的请求岂不是越积越多？这个问题先就放着不考虑。因为我感觉如果像 AppendEntries 后续可以一个请求补全所有 entries 的话，重复发之前的请求是没有意义的，而且也很混乱。



Raft 采用投票的方式来保证一个 candidate 只有拥有之前所有任期中已经提交的日志条目之后，才有可能赢得选举。一个 candidate 如果想要被选为 leader，那它就必须跟集群中超过半数的节点进行通信，这就意味这些节点中至少一个包含了所有已经提交的日志条目。如果 candidate 的日志至少跟过半的服务器节点一样新，那么它就一定包含了所有以及提交的日志条目，一旦有投票者自己的日志比 candidate 的还新，那么这个投票者就会拒绝该投票，该 candidate 也就不会赢得选举。对应 RequestVote RPC 中的这个：

![image-20220922220958310](https://beetpic.oss-cn-hangzhou.aliyuncs.com/img/image-20220922220958310.png)

**怎么判断谁的日志更新？**

单纯的看Term可以么？其实是不可以的，因为一个节点可以不断增加自己的Term，那么一个节点的Term更高并不意味着其日志更新。我们需要比较的是节点的最后一个日志是在什么Term产生的。

因此，采用下面的判定方法来判断两个节点 N1 和N2 的日志新旧程度。

1. 如果N1 的最后一条日志的Term >N2 的最后一条日志的Term，那么 N1 的日志更新；如果二者Term相等，则进入下一步；否则 N2 的日志更新。
2. 在一个Term中往往会有很多条日志，而这些日志则会被分配一个序号也就是index。那么如果 N1 的最后一条日志的index >N2 的最后一条日志的index，那么 N1 的日志更新；如果二者index相等，则一样新；否则 N2 的日志更新。

这里判断的时候我们可以回顾之前的日志匹配特性（Log Matching Property），在本文上面不远处。





leader 收到 client 的 command 后返回的全过程：

![image-20220924201353097](https://beetpic.oss-cn-hangzhou.aliyuncs.com/img/image-20220924201353097.png)

只要其他 follower 收到了（AppendEntries 的 reply 的 success 不为 false），后面就等它们自动 apply 就行，leader 这边就已经认为它们成功了，更新 nextIndex 和 matchIndex。如果超过半数的 server 都成功了，leader 更新 commitIndex，然后会因此触发 apply 到状态机，然后返回给 client 成功。

## Part 2C - persistence

## Part 2D - log compaction

