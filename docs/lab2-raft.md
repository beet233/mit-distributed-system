# lab2-raft

> 名词解释
>
> + term: 任期

目标

> A service calls `Make(peers,me,…)` to create a Raft peer. The peers argument is an array of network identifiers of the Raft peers (including this one), for use with RPC. The `me` argument is the index of this peer in the peers array. `Start(command)` asks Raft to start the processing to append the command to the replicated log. `Start()` should return immediately, without waiting for the log appends to complete. The service expects your implementation to send an `ApplyMsg` for each newly committed log entry to the `applyCh` channel argument to `Make()`.
>
> `raft.go` contains example code that sends an RPC (`sendRequestVote()`) and that handles an incoming RPC (`RequestVote()`). Your Raft peers should exchange RPCs using the labrpc Go package (source in `src/labrpc`). The tester can tell `labrpc` to delay RPCs, re-order them, and discard them to simulate various network failures. While you can temporarily modify `labrpc`, make sure your Raft works with the original `labrpc`, since that's what we'll use to test and grade your lab. Your Raft instances must interact only with RPC; for example, they are not allowed to communicate using shared Go variables or files.

目标（中文）

> 某服务调用 `Make(peers,me,…)` 来创造一个 Raft peer，peers 参数是 Raft peers 们的网络标识符数组（包括 me），`me` 参数是 peers 中当前 peer 的 index。`Start(command)` 让 Raft 开始处理一个 command，并加进它的复制 log 中。而 `Start()` 应该立刻返回，毕竟没有 log 嘛。服务希望当一个 log 被 commit 了后，通过 `applyCh` 这个 channel 发一条 `ApplyMsg ` 给它，这个 `applyCh` 是在 `Make` 中注册进去的。

## Part 2A - leader election

Task

> Implement Raft leader election and heartbeats (`AppendEntries` RPCs with no log entries). The goal for Part 2A is for a single leader to be elected, for the leader to remain the leader if there are no failures, and for a new leader to take over if the old leader fails or if packets to/from the old leader are lost. Run `go test -run 2A `to test your 2A code.

Hints

> + Fill in the `RequestVoteArgs` and `RequestVoteReply` structs. Modify `Make()` to create a background goroutine that will kick off leader election periodically by sending out `RequestVote` RPCs when it hasn't heard from another peer for a while. This way a peer will learn who is the leader, if there is already a leader, or become the leader itself. Implement the `RequestVote()` RPC handler so that servers will vote for one another.
>
> + To implement heartbeats, define an `AppendEntries` RPC struct (though you may not need all the arguments yet), and have the leader send them out periodically. Write an `AppendEntries` RPC handler method that resets the election timeout so that other servers don't step forward as leaders when one has already been elected.



## Part 2B - log

## Part 2C - persistence

## Part 2D - log compaction

