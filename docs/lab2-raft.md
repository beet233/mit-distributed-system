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

## Part 2C - persistence

## Part 2D - log compaction

