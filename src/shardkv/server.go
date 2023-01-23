package shardkv

import (
	"6.824/labrpc"
	"6.824/shardctrler"
	"github.com/sasha-s/go-deadlock"
	"log"
	"time"
)
import "6.824/raft"
import "6.824/labgob"

const FETCH_CONFIG_INTERVAL = 100

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
	ShardId int
	Storage map[string]string
}

type ShardKV struct {
	// 这里可以改成一把读写锁，应该对性能不错
	mu           deadlock.RWMutex
	me           int
	rf           *raft.Raft
	applyCh      chan raft.ApplyMsg
	make_end     func(string) *labrpc.ClientEnd
	gid          int
	ctrlers      []*labrpc.ClientEnd
	ctrlerClerk  *shardctrler.Clerk
	maxraftstate int // snapshot if log grows this big

	// Your definitions here.
	shards     map[int]Shard
	prevConfig shardctrler.Config
	currConfig shardctrler.Config
	persister  *raft.Persister
	// raft log index -> wait channel
	waitChs                map[int]chan WaitChResponse
	latestAppliedRequest   map[int64]int // clientId -> requestId
	latestAppliedRaftIndex int
}

type Shard struct {
	storage map[string]string
	status  ShardStatus
}

type ShardStatus int

const (
	WORKING = 0 // 正常工作
	PULLING = 1 // 等待从之前的 group 获取
	LEAVING = 2 // 等待新的 group 确认获取
	WAITING = 3 // 等待之前的 group 删除
)

type WaitChResponse struct {
	err   Err
	value string // for Get
}

func (kv *ShardKV) Get(args *GetArgs, reply *GetReply) {
	// Your code here.
}

func (kv *ShardKV) PutAppend(args *PutAppendArgs, reply *PutAppendReply) {
	// Your code here.
}

//
// the tester calls Kill() when a ShardKV instance won't
// be needed again. you are not required to do anything
// in Kill(), but it might be convenient to (for example)
// turn off debug output from this instance.
//
func (kv *ShardKV) Kill() {
	kv.rf.Kill()
	// Your code here, if desired.
}

func (kv *ShardKV) applyLoop() {

}

func (kv *ShardKV) fetchNextConfig() {
	for {
		kv.mu.Lock()
		readForNext := true
		for _, shard := range kv.shards {
			if shard.status != WORKING {
				readForNext = false
			}
		}
		if readForNext {
			currVersion := kv.currConfig.Num
			latestConfig := kv.ctrlerClerk.Query(currVersion + 1)
			if latestConfig.Num > currVersion {
				if latestConfig.Num != currVersion+1 {
					log.Fatalln("Jump through some versions!")
				}
				// TODO: 更新 config 并标记各个 shard 状态，用 raft.Start 进 applyLoop 完成
				// kv.currConfig = latestConfig
				// if kv.currConfig.Num == 1 {
				// } else {
				// }
			}
		}
		kv.mu.Unlock()
		time.Sleep(time.Millisecond * FETCH_CONFIG_INTERVAL)
	}
}

//
// servers[] contains the ports of the servers in this group.
//
// me is the index of the current server in servers[].
//
// the k/v server should store snapshots through the underlying Raft
// implementation, which should call persister.SaveStateAndSnapshot() to
// atomically save the Raft state along with the snapshot.
//
// the k/v server should snapshot when Raft's saved state exceeds
// maxraftstate bytes, in order to allow Raft to garbage-collect its
// log. if maxraftstate is -1, you don't need to snapshot.
//
// gid is this group's GID, for interacting with the shardctrler.
//
// pass ctrlers[] to shardctrler.MakeClerk() so you can send
// RPCs to the shardctrler.
//
// make_end(servername) turns a server name from a
// Config.Groups[gid][i] into a labrpc.ClientEnd on which you can
// send RPCs. You'll need this to send RPCs to other groups.
//
// look at client.go for examples of how to use ctrlers[]
// and make_end() to send RPCs to the group owning a specific shard.
//
// StartServer() must return quickly, so it should start goroutines
// for any long-running work.
//
func StartServer(servers []*labrpc.ClientEnd, me int, persister *raft.Persister, maxraftstate int, gid int, ctrlers []*labrpc.ClientEnd, make_end func(string) *labrpc.ClientEnd) *ShardKV {
	// call labgob.Register on structures you want
	// Go's RPC library to marshall/unmarshall.
	labgob.Register(Op{})

	kv := new(ShardKV)
	kv.me = me
	kv.maxraftstate = maxraftstate
	kv.make_end = make_end
	kv.gid = gid
	kv.ctrlers = ctrlers

	// Your initialization code here.
	kv.ctrlerClerk = shardctrler.MakeClerk(ctrlers)
	kv.shards = make(map[int]Shard)
	// 初始化为和 shardctrler 一模一样的 invalid config 0
	kv.currConfig = shardctrler.Config{Groups: map[int][]string{}}
	kv.prevConfig = kv.currConfig
	kv.persister = persister
	kv.waitChs = make(map[int]chan WaitChResponse)
	kv.latestAppliedRequest = make(map[int64]int)
	kv.latestAppliedRaftIndex = 0

	kv.applyCh = make(chan raft.ApplyMsg)
	kv.rf = raft.Make(servers, me, persister, kv.applyCh)

	go kv.fetchNextConfig()
	go kv.applyLoop()
	return kv
}
