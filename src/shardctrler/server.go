package shardctrler

import (
	"6.824/raft"
	"fmt"
	"log"
	"time"
)
import "6.824/labrpc"
import "sync"
import "6.824/labgob"

const Debug = true

func DPrintf(format string, a ...interface{}) (n int, err error) {
	if Debug {
		rightHalf := fmt.Sprintf(format, a...)
		log.Printf("kv | %s", rightHalf)
	}
	return
}

type ShardCtrler struct {
	mu      sync.Mutex
	me      int
	rf      *raft.Raft
	applyCh chan raft.ApplyMsg

	// Your data here.

	configs []Config // indexed by config num
	// raft log index -> wait channel
	waitChs                map[int]chan WaitChResponse
	latestAppliedRequest   map[int64]int // clientId -> requestId
	latestAppliedRaftIndex int
}

const TimeOut = 200

type OpType int

const (
	JOIN  = 0
	LEAVE = 1
	MOVE  = 2
	QUERY = 3
)

type Op struct {
	// Your data here.
	Type      OpType
	ClientId  int64
	RequestId int
	JoinArgs  JoinArgs
	LeaveArgs LeaveArgs
	MoveArgs  MoveArgs
	QueryArgs QueryArgs
}

type WaitChResponse struct {
	err Err
	// 某些 reply 需要的值加在这里，由 applyLoop 应用到状态机之后返回到 RPC Handler
	config Config
}

func (sc *ShardCtrler) Join(args *JoinArgs, reply *JoinReply) {
	// Your code here.
	raftIndex, _, isLeader := sc.rf.Start(Op{Type: JOIN, JoinArgs: *args})
	if !isLeader {
		// TODO: 考虑 reply 的 Err 除了 OK 还有没有别的需求
		reply.WrongLeader = true
	} else {
		reply.WrongLeader = false
		sc.mu.Lock()
		waitCh, exist := sc.waitChs[raftIndex]
		if exist {
			log.Fatalf("kv | server %d try to get a existing waitCh\n", sc.me)
		}
		sc.waitChs[raftIndex] = make(chan WaitChResponse, 1)
		waitCh = sc.waitChs[raftIndex]
		sc.mu.Unlock()
		select {
		case <-time.After(time.Millisecond * TimeOut):
			reply.WrongLeader = true
		case response := <-waitCh:
			// TODO: 似乎没什么事可做，err 有什么用吗？
			reply.Err = response.err
		}
	}
}

func (sc *ShardCtrler) Leave(args *LeaveArgs, reply *LeaveReply) {
	// Your code here.
}

func (sc *ShardCtrler) Move(args *MoveArgs, reply *MoveReply) {
	// Your code here.
}

func (sc *ShardCtrler) Query(args *QueryArgs, reply *QueryReply) {
	// Your code here.
}

//
// the tester calls Kill() when a ShardCtrler instance won't
// be needed again. you are not required to do anything
// in Kill(), but it might be convenient to (for example)
// turn off debug output from this instance.
//
func (sc *ShardCtrler) Kill() {
	sc.rf.Kill()
	// Your code here, if desired.
}

// needed by shardkv tester
func (sc *ShardCtrler) Raft() *raft.Raft {
	return sc.rf
}

func (sc *ShardCtrler) applyLoop() {
	for {
		applyMsg := <-sc.applyCh
		if applyMsg.CommandValid {
			if applyMsg.CommandIndex <= sc.latestAppliedRaftIndex {
				DPrintf("shardctrler server %d get older command from applyCh\n", sc.me)
				continue
			}
			op := applyMsg.Command.(Op) // 强制类型转换成 Op
			waitChResponse := WaitChResponse{}
			// TODO: 判断是否是重复请求，根据 op 中的 requestId 和 clientId
			// TODO: 实际执行策略 ↓
			switch op.Type {
			case JOIN:

			case LEAVE:

			case MOVE:

			case QUERY:

			}
			sc.latestAppliedRaftIndex = applyMsg.CommandIndex
		} else {
			// 不使用也不考虑 snapshot，因为 config change 量比较小
		}
	}
}

//
// servers[] contains the ports of the set of
// servers that will cooperate via Raft to
// form the fault-tolerant shardctrler service.
// me is the index of the current server in servers[].
//
func StartServer(servers []*labrpc.ClientEnd, me int, persister *raft.Persister) *ShardCtrler {
	sc := new(ShardCtrler)
	sc.me = me

	sc.configs = make([]Config, 1)
	sc.configs[0].Groups = map[int][]string{}

	labgob.Register(Op{})
	sc.applyCh = make(chan raft.ApplyMsg)
	sc.rf = raft.Make(servers, me, persister, sc.applyCh)

	// Your code here.
	// 因为这里也是个基于 raft 的，所以也是需要一个处理 applyCh 的线程
	go sc.applyLoop()
	return sc
}
