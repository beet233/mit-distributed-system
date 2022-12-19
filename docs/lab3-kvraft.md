# lab3-kvraft

## Part 3A - Key/value service without snapshots

我们需要基于 Raft 实现一个容灾备份的 Key-Value 存储，并保证其**线性一致性**。作为这样强一致的存储，自然我们需要给出简单易用的接口，让使用这个存储就像使用一个单机存储一样自然。

用户将通过 client 来访问这个服务，而这个服务由多个 server 组成，这些 server 间的联系由底层的每个 server 对应一个的 raft replica 来维系。

```
-------------                                  ----------         
|     Client   |      --------- \            |  Server |         ----------
|  ---------  |      to leader  >         ----------         |  Server |
|  |  Clerk  |  |      --------- /           ----------          ----------
|  ---------  |                                 |  Server |
-------------                                 ----------
```

> 每个 client 通过一个 `struct Clerk` 来访问 server。

因为要保证线性一致性，所以从副本读是不现实的（提高并发读的能力请参考 ***ZooKeeper***），于是乎所有的读写请求都由 raft leader 对应的 server 来承担。当然，这样的结果是，server 越多，实际上达成一致的代价就越高，速度就更慢，除 leader 以外的 server 只能承担容灾的作用，但这里的优化不在本实验的考虑范围内。

### Main Idea

#### Basic Implement

先讲讲最基本的实现：

+ client 通过一个 clerk 结构来向 server 发送 Get/Put/Append 等 RPC 请求。
+ server 收到后，如果背后的 raft 不是 leader，返回 ErrWrongLeader，让 client 另寻高就。
+ 如果 server 背后的 raft 是 leader，用 raft 的 Start 开始对这次的请求进行共识，计入 log。
+ 在计入 log 后，raft 通过 applyCh 将消息传达过来，server 将这个操作真正执行到状态机中，并返回 client 发来的 RPC 的 reply。

看起来不是很复杂，其实还是有一些细节。

首先，applyCh 这东西肯定是用一个线程来保持接收，因为不同客户端并发地发送请求，比较难以保证最后形成 log 以及从 applyCh 里返回的先后顺序，用 RPC handler 线程直接读 applyCh 就不太合理。那么单独一个读取 applyCh 的线程，怎么把接收到的 applyMsg 告诉各个 RPC handler 线程呢？

> 这里考虑到，applyMsg 里有 raft 内这次 apply 的 log 的 index，而且 Start 函数也会返回一个预期出现的 index，并且 Start 函数返回时，这条 log 虽然还没 commit，但也已经进入 leader 的 logs 中了。
>
> 所以我们自然可以用 index 来找到对应各个 RPC handler 的 channel，采用 `waitChs              map[int]chan WaitChResponse`。
>
> 注意，不只是 leader 收到 applyMsg 要应用到状态机，follower 也要。而且 followers 是没有 RPC handler 在进行的。所以 apply 这个步骤要在接收 applyCh 的 applyLoop 里做。如果是 leader，还要把结果 / Err 打包传进 waitChs，让 server 回复 RPC reply。

#### Remember lastLeader

客户端没必要每次都去找 raft leader，毕竟也没那么容易坏。我们可以用一个状态来记住上一次的 leader，如果发给他发现不对，那再试试别的也不迟。

#### Time Out  and Retry

凡事总有意外，server 必会超时。leader 服务器直接崩了？网络出了点毛病，raft 共识失败了？都需要对应的处理。

先看 client 这边：

```go
func (ck *Clerk) Get(key string) string {

	// You will have to modify this function.
	ck.mu.Lock()
	defer ck.mu.Unlock()
	result := ""
	// call lastLeader 的 GET RPC
	for ; ; ck.IncrementLastLeader() {
		args := GetArgs{Key: key, ClientId: ck.clientId, RequestId: ck.requestId}
		reply := GetReply{}
		ok := ck.servers[ck.lastLeader].Call("KVServer.Get", &args, &reply)
		if ok {
			if reply.Err == OK {
				result = reply.Value
				break
			} else if reply.Err == ErrNoKey {
				result = ""
				break
			}
			// 其他错误直接继续循环
		}
	}
	ck.requestId += 1
	return result
}
```

可以看到，网络错误/直接没有返回，都将直接尝试下一个 server。

再看 server 这边：

```go
func (kv *KVServer) Get(args *GetArgs, reply *GetReply) {
	// Your code here.
	// 如果我背后的 raft replica 并不是 leader，那直接返回一个 Err
	_, isLeader := kv.rf.GetState()
	if !isLeader {
		reply.Err = ErrWrongLeader
	} else {
		// 把这次请求打包成一个 Op 发给 raft 的 Start
		// 解释一下为什么不直接返回 storage 的内容而是要把 get 也发给 raft 做共识：
		// 因为我们要保证线性一致性，所以把 get 发给 raft，等收到这次的 applyMsg 后，
		// 可以保证之前的所有命令都已经执行。
		raftIndex, _, isLeader := kv.rf.Start(Op{Type: GET, Key: args.Key, ClientId: args.ClientId, RequestId: args.RequestId})
		if !isLeader {
			reply.Err = ErrWrongLeader
		} else {
			// 因为这个 Get 是个 RPC Handler，是要返回 reply 的，所以要等到 applyCh 传回关于这条指令的 msg 才行，建立以下通信机制
			kv.mu.Lock()
			waitCh, exist := kv.waitChs[raftIndex]
			if exist {
				log.Fatalf("kv | server %d try to get a existing waitCh\n", kv.me)
			}
			kv.waitChs[raftIndex] = make(chan WaitChResponse, 1)
			waitCh = kv.waitChs[raftIndex]
			kv.mu.Unlock()
			// 选择在 server 端来判断超时，如果超时了还没返回，共识失败，认为 ErrWrongLeader
			select {
			case <-time.After(time.Millisecond * TimeOut):
				DPrintf("server %d timeout when handling Get\n", kv.me)
				reply.Err = ErrWrongLeader
			case response := <-waitCh:
				reply.Value = response.value
				reply.Err = response.err
			}
			// 用完后把 waitCh 删掉
			kv.mu.Lock()
			delete(kv.waitChs, raftIndex)
			kv.mu.Unlock()
		}
	}
}
```

用 select 来做选择，哪个 case 的 IO 先完成就走哪个 case。如果 raft 一定时间内没有完成共识，那就直接返回 ErrWrongLeader，让 client 另寻 leader。当然，其实这个超时放在 client 那边做也是可以的。

#### Prevent Duplicated Process

如果某个 leader 在 commit 了一条日志，但是还没返回给 applyCh 的时候崩了，此时这条 log 已经达成共识，在别的 server 将被应用，在这边也吃枣恢复后应用。然而，因为我们的超时重试机制，同一条操作会被再次发来。这个重试机制是 client 自动在做的，并不是客户按的。我们需要保证这件事只发生一次。

于是，我们需要在 client 发送请求时就打上唯一的记号。当状态机看到同样的记号时，就不去执行它。

因为 client 数量在测试里不多，直接用 int64 的随机数生成一个 clientId。然后对一个 client 的请求依次递增序号 requestId。server 将记忆一个 client 当前最新被 applied 的 requestId。如果这个 client 又发来 <= 已经 applied 的 requestId 的请求，那就作为重复的请求处理。

+ 对于读请求，直接按最新的 kv 给它读。
+ 对于写请求，直接返回 OK 而不重复执行。

#### Linearizability

这样做下来，凭什么是线性一致的呢？

凭的是每次都写入 raft log 并被 commit 了再执行，即使是一个简单的 get 也如此。当一个操作被 raft commit 了，这就能够保证，它前面的所有操作都将在这次操作之前执行完毕。比如一个 get，当比它更早的请求已经被处理完毕返回客户端时，这些请求一定已经在 raft log 中，所以 get 一定可以读到他们作用的结果。

而再举个例子，一个 get（1号 get） 还没返回，另一个 get（2号 get） 又发出来了，可能出于种种原因，1号 get 要重试，而 2 号 get 不用，导致 1 号 get 可能拿到比 2 号 get 更新的值，而且它的 raft log 甚至可能在 2 号之前。然而，我们需要知道这是合理的，线性一致的。读请求是一个系统向外界提供观测的通道，当两个请求从发出到返回，有相互重合的时间段时，谁读到更新的数据都是合理的，都是可能满足线性一致性的。是不是真的满足线性一致性，只有：当某些请求已经返回给客户端了，他们的结果已经被观测到了，那在他们返回之后发出的请求，一定要也能够观测到前面这些请求的影响。