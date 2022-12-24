package kvraft

import (
	"6.824/labgob"
	"6.824/labrpc"
	"6.824/raft"
	"bytes"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"
)

const Debug = true

// 因为有部分测试如 TestOnePartition3A 会在一定时间内就去检查结果
// 如果重试得太慢，就会 fail，而重试快一些也无所谓，有防止 duplicated 机制
const TimeOut = 200

func DPrintf(format string, a ...interface{}) (n int, err error) {
	if Debug {
		rightHalf := fmt.Sprintf(format, a...)
		log.Printf("kv | %s", rightHalf)
	}
	return
}

type WaitChResponse struct {
	op    Op
	err   Err
	value string // for Get
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
	storage   map[string]string
	persister *raft.Persister
	// raft log index -> wait channel
	waitChs                map[int]chan WaitChResponse
	latestAppliedRequest   map[int64]int // clientId -> requestId
	latestAppliedRaftIndex int
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

func (kv *KVServer) PutAppend(args *PutAppendArgs, reply *PutAppendReply) {
	// Your code here.
	// 如果我背后的 raft replica 并不是 leader，那直接返回一个 Err
	_, isLeader := kv.rf.GetState()
	if !isLeader {
		reply.Err = ErrWrongLeader
	} else {
		DPrintf("server %d handle PutAppend key: %s, value: %s\n", kv.me, args.Key, args.Value)
		// 把这次请求打包成一个 Op 发给 raft 的 Start
		var opType OpType
		if args.Op == "Put" {
			opType = PUT
		} else {
			opType = APPEND
		}
		raftIndex, _, isLeader := kv.rf.Start(Op{Type: opType, Key: args.Key, Value: args.Value, ClientId: args.ClientId, RequestId: args.RequestId})
		if !isLeader {
			reply.Err = ErrWrongLeader
		} else {
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
				DPrintf("server %d timeout when handling PutAppend\n", kv.me)
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
			if applyMsg.CommandIndex <= kv.latestAppliedRaftIndex {
				DPrintf("server %d get older command from applyCh\n", kv.me)
				continue
			}
			op := applyMsg.Command.(Op)
			waitChResponse := WaitChResponse{op: op}
			// 需要注意处理重复的请求
			switch op.Type {
			case GET:
				kv.mu.Lock()
				// 对于读请求，要保证线性一致性，其实只要共识达成了就行，保证（在读请求发出前就完成的操作都被读到）
				// 在读请求返回前，如果因为发生了问题而间隔较长，返回的内容其实是灵活的，在满足线性一致性的前提下取决于实现
				// 我感觉读请求不需要做额外处理，如果是一次重试，那还是直接读现在的
				_, exist := kv.latestAppliedRequest[op.ClientId]
				if !exist {
					kv.latestAppliedRequest[op.ClientId] = -1
				}
				if op.RequestId > kv.latestAppliedRequest[op.ClientId] {
					kv.latestAppliedRequest[op.ClientId] = op.RequestId
				}
				value, keyExist := kv.storage[applyMsg.Command.(Op).Key]
				kv.mu.Unlock()
				if !keyExist {
					waitChResponse.err = ErrNoKey
				} else {
					waitChResponse.err = OK
					waitChResponse.value = value
				}
			case PUT:
				kv.mu.Lock()
				DPrintf("server %d put key: %s, value: %s\n", kv.me, op.Key, op.Value)
				_, exist := kv.latestAppliedRequest[op.ClientId]
				if !exist {
					kv.latestAppliedRequest[op.ClientId] = -1
				}
				if op.RequestId > kv.latestAppliedRequest[op.ClientId] {
					kv.latestAppliedRequest[op.ClientId] = op.RequestId
					kv.storage[op.Key] = op.Value
				}
				// 如果是重复的请求，就不做实际操作了，返回 OK 就行
				kv.mu.Unlock()
				waitChResponse.err = OK
			case APPEND:
				kv.mu.Lock()
				DPrintf("server %d append key: %s, value: %s\n", kv.me, op.Key, op.Value)
				_, exist := kv.latestAppliedRequest[op.ClientId]
				if !exist {
					kv.latestAppliedRequest[op.ClientId] = -1
				}
				if op.RequestId > kv.latestAppliedRequest[op.ClientId] {
					kv.latestAppliedRequest[op.ClientId] = op.RequestId
					value, keyExist := kv.storage[op.Key]
					if keyExist {
						kv.storage[op.Key] = value + op.Value
					} else {
						kv.storage[op.Key] = op.Value
					}
				}
				// 如果是重复的请求，就不做实际操作了，返回 OK 就行
				kv.mu.Unlock()
				waitChResponse.err = OK
			}
			kv.latestAppliedRaftIndex = applyMsg.CommandIndex
			if kv.maxraftstate != -1 && kv.persister.RaftStateSize() >= kv.maxraftstate {
				// 序列化 storage 和 latestAppliedRequest，调用 rf.Snapshot
				kv.rf.Snapshot(applyMsg.CommandIndex, kv.serialize())
			}
			// 如果返回了，却找不到这个 waitCh 了，那说明超时被放弃了，直接不传就行，下次遇到同样的就不执行
			kv.mu.Lock()
			waitCh, exist := kv.waitChs[applyMsg.CommandIndex]
			kv.mu.Unlock()
			// 注意，其实只有 leader 有 waitCh，别的 follower 虽然也不断从 applyCh 里接收，但是是没有 waitCh 的
			// 同理，follower 们的 apply 到 storage 的过程实质上要在 applyLoop 进行
			if exist {
				waitCh <- waitChResponse
			} else {
				// DPrintf("server %d found no waitCh for raftIndex %d\n", kv.me, applyMsg.CommandIndex)
			}
		} else {
			// 如果快照比状态机还新，那直接更新到快照。
			// 如果快照比当前快照新，那其实 raft 那边在传过来前就已经切割完了 log 并存好了 raft state 和 service 的 snapshot，这边不用干。
			if applyMsg.SnapshotIndex > kv.latestAppliedRaftIndex {
				DPrintf("server %d get snapshot newer than state machine\n", kv.me)
				kv.deserialize(applyMsg.Snapshot)
				// kv.latestAppliedRaftIndex = applyMsg.SnapshotIndex
			}
		}
	}
}

func (kv *KVServer) serialize() []byte {
	DPrintf("server %d serialize to a snapshot\n", kv.me)
	kv.mu.Lock()
	defer kv.mu.Unlock()
	w := new(bytes.Buffer)
	e := labgob.NewEncoder(w)
	e.Encode(kv.latestAppliedRaftIndex)
	e.Encode(kv.storage)
	e.Encode(kv.latestAppliedRequest)
	data := w.Bytes()
	return data
}

func (kv *KVServer) deserialize(snapshot []byte) {
	kv.mu.Lock()
	defer kv.mu.Unlock()
	if snapshot == nil || len(snapshot) < 1 {
		DPrintf("server %d has no snapshot to recover\n", kv.me)
		return
	}

	DPrintf("server %d read persister to recover\n", kv.me)

	r := bytes.NewBuffer(snapshot)
	d := labgob.NewDecoder(r)

	var persistLatestAppliedRaftIndex int
	var persistStorage map[string]string
	var persistLatestAppliedRequest map[int64]int

	if d.Decode(&persistLatestAppliedRaftIndex) != nil || d.Decode(&persistStorage) != nil || d.Decode(&persistLatestAppliedRequest) != nil {
		DPrintf("server %d read persister error\n", kv.me)
	} else {
		kv.latestAppliedRaftIndex = persistLatestAppliedRaftIndex
		kv.storage = persistStorage
		kv.latestAppliedRequest = persistLatestAppliedRequest
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
	kv.persister = persister
	kv.storage = make(map[string]string)
	kv.waitChs = make(map[int]chan WaitChResponse)
	kv.latestAppliedRequest = make(map[int64]int)
	snapshot := persister.ReadSnapshot()
	kv.deserialize(snapshot)
	go kv.applyLoop()
	return kv
}
