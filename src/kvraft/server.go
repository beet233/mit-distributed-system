package kvraft

import (
	"6.824/labgob"
	"6.824/labrpc"
	"6.824/raft"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"
)

const Debug = false

const TimeOut = 500

func DPrintf(format string, a ...interface{}) (n int, err error) {
	if Debug {
		rightHalf := fmt.Sprintf(format, a...)
		log.Printf("kv | %s", rightHalf)
	}
	return
}

const (
	GET    = 0
	PUT    = 1
	APPEND = 2
)

type OpType int

type Op struct {
	// Your definitions here.
	// Field names must start with capital letters,
	// otherwise RPC will break.
	Type      OpType
	Key       string
	Value     string
	ClientId  int64
	RequestId int
}

type KVServer struct {
	mu      sync.Mutex
	me      int
	rf      *raft.Raft
	applyCh chan raft.ApplyMsg
	dead    int32 // set by Kill()

	maxraftstate int // snapshot if log grows this big

	// Your definitions here.
	storage map[string]string
	// raft log index -> wait channel
	waitChs map[int]chan Op
}

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
			kv.waitChs[raftIndex] = make(chan Op, 1)
			waitCh = kv.waitChs[raftIndex]
			kv.mu.Unlock()
			// 选择在 server 端来判断超时，如果超时了还没返回，共识失败，认为 ErrWrongLeader
			select {
			case <-time.After(time.Millisecond * TimeOut):
				reply.Err = ErrWrongLeader
			case committedOp := <-waitCh:
				kv.mu.Lock()
				value, keyExist := kv.storage[committedOp.Key]
				kv.mu.Unlock()
				if !keyExist {
					reply.Err = ErrNoKey
				} else {
					reply.Err = OK
					reply.Value = value
				}
			}
			// 用完后把 waitCh 删掉
			kv.mu.Lock()
			delete(kv.waitChs, raftIndex)
			kv.mu.Unlock()
		}
	}
}

func (kv *KVServer) PutAppend(args *PutAppendArgs, reply *PutAppendReply) {
	// Your code here.
	// 如果我背后的 raft replica 并不是 leader，那直接返回一个 Err
	_, isLeader := kv.rf.GetState()
	if !isLeader {
		reply.Err = ErrWrongLeader
	} else {
		// 把这次请求打包成一个 Op 发给 raft 的 Start
	}
}

//
// the tester calls Kill() when a KVServer instance won't
// be needed again. for your convenience, we supply
// code to set rf.dead (without needing a lock),
// and a killed() method to test rf.dead in
// long-running loops. you can also add your own
// code to Kill(). you're not required to do anything
// about this, but it may be convenient (for example)
// to suppress debug output from a Kill()ed instance.
//
func (kv *KVServer) Kill() {
	atomic.StoreInt32(&kv.dead, 1)
	kv.rf.Kill()
	// Your code here, if desired.
}

func (kv *KVServer) killed() bool {
	z := atomic.LoadInt32(&kv.dead)
	return z == 1
}

// 负责接收 applyCh 并分发给各个 rpc handler 线程
func (kv *KVServer) applyLoop() {
	for {
		applyMsg := <-kv.applyCh
		if applyMsg.CommandValid {
			// 如果返回了，却找不到这个 waitCh 了，那说明超时被放弃了，直接丢掉就行
			waitCh, exist := kv.waitChs[applyMsg.CommandIndex]
			log.Printf("kv | server %d found waitCh deleted\n", kv.me)
			if exist {
				waitCh <- applyMsg.Command.(Op)
			}
		}
	}
}

//
// servers[] contains the ports of the set of
// servers that will cooperate via Raft to
// form the fault-tolerant key/value service.
// me is the index of the current server in servers[].
// the k/v server should store snapshots through the underlying Raft
// implementation, which should call persister.SaveStateAndSnapshot() to
// atomically save the Raft state along with the snapshot.
// the k/v server should snapshot when Raft's saved state exceeds maxraftstate bytes,
// in order to allow Raft to garbage-collect its log. if maxraftstate is -1,
// you don't need to snapshot.
// StartKVServer() must return quickly, so it should start goroutines
// for any long-running work.
//
func StartKVServer(servers []*labrpc.ClientEnd, me int, persister *raft.Persister, maxraftstate int) *KVServer {
	// call labgob.Register on structures you want
	// Go's RPC library to marshall/unmarshall.
	labgob.Register(Op{})

	kv := new(KVServer)
	kv.me = me
	kv.maxraftstate = maxraftstate

	// You may need initialization code here.

	kv.applyCh = make(chan raft.ApplyMsg)
	kv.rf = raft.Make(servers, me, persister, kv.applyCh)

	// You may need initialization code here.

	kv.storage = make(map[string]string)
	go kv.applyLoop()
	return kv
}
