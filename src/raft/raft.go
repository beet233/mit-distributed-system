package raft

//
// this is an outline of the API that raft must expose to
// the service (or tester). see comments below for
// each of these functions for more details.
//
// rf = Make(...)
//   create a new Raft server.
// rf.Start(command interface{}) (index, term, isleader)
//   start agreement on a new log entry
// rf.GetState() (term, isLeader)
//   ask a Raft for its current term, and whether it thinks it is leader
// ApplyMsg
//   each time a new entry is committed to the log, each Raft peer
//   should send an ApplyMsg to the service (or tester)
//   in the same server.
//

import (
	"6.824/labgob"
	"bytes"
	"fmt"
	"log"
	"math/rand"
	"runtime/debug"
	"time"

	//	"bytes"

	"sync/atomic"

	//	"6.824/labgob"
	"6.824/labrpc"
	"github.com/sasha-s/go-deadlock"
)

//
// as each Raft peer becomes aware that successive log entries are
// committed, the peer should send an ApplyMsg to the service (or
// tester) on the same server, via the applyCh passed to Make(). set
// CommandValid to true to indicate that the ApplyMsg contains a newly
// committed log entry.
//
// in part 2D you'll want to send other kinds of messages (e.g.,
// snapshots) on the applyCh, but set CommandValid to false for these
// other uses.
//
type ApplyMsg struct {
	CommandValid bool
	Command      interface{}
	CommandIndex int

	// For 2D:
	SnapshotValid bool
	Snapshot      []byte
	SnapshotTerm  int
	SnapshotIndex int
}

type LogEntry struct {
	Term    int
	Command interface{}
}

//
// A Go object implementing a single Raft peer.
//
type Raft struct {
	// mu        sync.Mutex          // Lock to protect shared access to this peer's state
	peers     []*labrpc.ClientEnd // RPC end points of all peers
	persister *Persister          // Object to hold this peer's persisted state
	me        int                 // this peer's index into peers[]
	dead      int32               // set by Kill()

	// Your data here (2A, 2B, 2C).
	// Look at the paper's Figure 2 for a description of what
	// state a Raft server must maintain.
	raftState *RaftState

	log               []LogEntry
	lastIncludedIndex int
	lastIncludedTerm  int
	logMutex          deadlock.RWMutex

	commitIndex int
	commitMutex deadlock.RWMutex
	lastApplied int

	// 用一个锁管理这俩
	nextIndex    []int
	matchIndex   []int
	peerLogMutex deadlock.RWMutex

	leaderInitDone  bool
	leaderInitMutex deadlock.Mutex

	// apply notify channel
	applyNotifyCh chan int

	// apply channel
	applyCh chan ApplyMsg

	// some log flags with wrapped log func
	electionDebug       bool
	logReplicationDebug bool
	persistenceDebug    bool
	snapshotDebug       bool
}

// return currentTerm and whether this server
// believes it is the leader.
func (rf *Raft) GetState() (int, bool) {

	var term int
	var isLeader bool
	// Your code here (2A).
	rf.raftState.rLock()
	term = rf.raftState.currentTerm
	isLeader = rf.raftState.isState(leaderState)
	rf.raftState.rUnlock()
	return term, isLeader
}

func (rf *Raft) logWithRaftStatus(format string, vars ...interface{}) {
	var stateString string
	state := rf.raftState.state
	switch state {
	case candidateState:
		stateString = "candidate"
		break
	case leaderState:
		stateString = "leader"
		break
	case followerState:
		stateString = "follower"
		break
	default:
		log.Fatal("undefined raft state.")
		break
	}
	rightHalf := fmt.Sprintf(format, vars...)
	log.Printf("server %d %s in term %d votedFor %d commitIndex %d lastApplied %d leaderInitDone %v lastIncludedIndex %d lastIncludedTerm %v | %s", rf.me, stateString, rf.raftState.currentTerm, rf.raftState.votedFor, rf.commitIndex, rf.lastApplied, rf.leaderInitDone, rf.lastIncludedIndex, rf.lastIncludedTerm, rightHalf)
}

func (rf *Raft) electionLog(format string, vars ...interface{}) {
	if rf.electionDebug {
		rf.logWithRaftStatus(format, vars...)
	}
}

func (rf *Raft) logReplicationLog(format string, vars ...interface{}) {
	if rf.logReplicationDebug {
		rf.logWithRaftStatus(format, vars...)
	}
}

func (rf *Raft) persistenceLog(format string, vars ...interface{}) {
	if rf.persistenceDebug {
		rf.logWithRaftStatus(format, vars...)
	}
}

func (rf *Raft) snapshotLog(format string, vars ...interface{}) {
	if rf.snapshotDebug {
		rf.logWithRaftStatus(format, vars...)
	}
}

//
// save Raft's persistent state to stable storage,
// where it can later be retrieved after a crash and restart.
// see paper's Figure 2 for a description of what should be persistent.
//
// 内部不锁，外部来锁
func (rf *Raft) persist() {
	// Your code here (2C).
	rf.persistenceLog("persist states and logs\n")
	w := new(bytes.Buffer)
	e := labgob.NewEncoder(w)
	// e.Encode(rf.xxx)
	// e.Encode(rf.yyy)
	e.Encode(rf.raftState.currentTerm)
	e.Encode(rf.raftState.votedFor)
	e.Encode(rf.lastIncludedIndex)
	e.Encode(rf.lastIncludedTerm)
	e.Encode(len(rf.log))
	for _, entry := range rf.log {
		e.Encode(entry)
	}
	//e.Encode(rf.log)
	data := w.Bytes()
	rf.persister.SaveRaftState(data)
}

//
// restore previously persisted state.
//
func (rf *Raft) readPersist(data []byte) {
	if data == nil || len(data) < 1 { // bootstrap without any state?
		return
	}
	// Your code here (2C).
	// Example:
	r := bytes.NewBuffer(data)
	d := labgob.NewDecoder(r)
	var currentTerm int
	var votedFor int
	var lastIncludedIndex int
	var lastIncludedTerm int
	var logLength int
	var logEntries []LogEntry
	if d.Decode(&currentTerm) != nil ||
		d.Decode(&votedFor) != nil ||
		d.Decode(&lastIncludedIndex) != nil ||
		d.Decode(&lastIncludedTerm) != nil ||
		d.Decode(&logLength) != nil {
		log.Fatalln("failed deserialization of term or votedFor or lastIncludedIndex or lastIncludedTerm or logLength.")
	} else {
		rf.persistenceLog("log length: %d\n", logLength)
		// decode log
		logEntries = make([]LogEntry, logLength)
		for i := 0; i < logLength; i++ {
			if d.Decode(&logEntries[i]) != nil {
				log.Fatalln("failed deserialization of log.")
			}
		}
		rf.raftState.currentTerm = currentTerm
		rf.raftState.votedFor = votedFor
		rf.lastIncludedIndex = lastIncludedIndex
		rf.lastIncludedTerm = lastIncludedTerm
		rf.log = logEntries
	}
}

// need to lock logMutex outside by caller, return max valid log index + 1
func (rf *Raft) getLogLength() int {
	return rf.lastIncludedIndex + len(rf.log)
}

func (rf *Raft) getTermOfLog(index int) int {
	if index >= rf.getLogLength() {
		debug.PrintStack()
		log.Fatalf("getTermOfLog: server %d does not have log %d yet\n", rf.me, index)
	}
	if index < rf.lastIncludedIndex {
		debug.PrintStack()
		log.Fatalf("getTermOfLog: server %d has save log %d to snapshot\n", rf.me, index)
	} else if index == rf.lastIncludedIndex {
		return rf.lastIncludedTerm
	} else {
		return rf.log[index-rf.lastIncludedIndex].Term
	}
	return -1
}

func (rf *Raft) getLog(index int) LogEntry {
	if index >= rf.getLogLength() {
		log.Fatalf("getLog: server %d does not have log %d yet\n", rf.me, index)
	}
	if index <= rf.lastIncludedIndex {
		log.Fatalf("getLog: server %d has save log %d to snapshot\n", rf.me, index)
	} else {
		return rf.log[index-rf.lastIncludedIndex]
	}
	return LogEntry{}
}

// start 和 end 为真正的 index，即加上快照的 lastIncludedIndex 后的，不包括 end log
func (rf *Raft) getLogs(start int, end int) []LogEntry {
	if end > rf.getLogLength() {
		log.Fatalf("getLogs: server %d getLogs end %d overflow\n", rf.me, end)
	}
	if start <= rf.lastIncludedIndex {
		log.Fatalf("getLogs: server %d has save log %d to snapshot\n", rf.me, start)
	}
	return rf.log[start-rf.lastIncludedIndex : end-rf.lastIncludedIndex]
}

// start 为真正的 index，即加上快照的 lastIncludedIndex 后的
func (rf *Raft) getLogsFrom(start int) []LogEntry {
	return rf.getLogs(start, rf.getLogLength())
}

//
// A service wants to switch to snapshot.  Only do so if Raft hasn't
// have more recent info since it communicate the snapshot on applyCh.
//
func (rf *Raft) CondInstallSnapshot(lastIncludedTerm int, lastIncludedIndex int, snapshot []byte) bool {

	// Your code here (2D).

	return true
}

// the service says it has created a snapshot that has
// all info up to and including index. this means the
// service no longer needs the log through (and including)
// that index. Raft should now trim its log as much as possible.
func (rf *Raft) Snapshot(index int, snapshot []byte) {
	// Your code here (2D).
	rf.snapshotLog("start snapshot (to index %d)\n", index)
	if rf.killed() {
		return
	}
	// 将 log 砍到从 index 开始的剩下部分，并将快照保存进 persister
	// 这里用 defer 可以避免每个退出的地方都要解锁的问题
	rf.raftState.rLock()
	rf.snapshotLog("snapshot raft state lock success\n")
	defer rf.raftState.rUnlock()
	rf.logMutex.Lock()
	rf.snapshotLog("snapshot log lock success\n")
	defer rf.logMutex.Unlock()
	rf.commitMutex.RLock()
	rf.snapshotLog("snapshot commit lock success\n")
	defer rf.commitMutex.RUnlock()
	rf.snapshotLog("snapshot all lock success\n")
	if rf.lastIncludedIndex >= index {
		rf.snapshotLog("log until index %d has been snapshot before\n", index)
		rf.snapshotLog("snapshot has saved log until index %d\n", rf.lastIncludedIndex)
		return
	}
	if rf.commitIndex < index {
		rf.snapshotLog("log until index %d has not been committed!\n", index)
		return
	}
	if rf.lastApplied < index {
		rf.snapshotLog("log until index %d has not been applied!\n", index)
		return
	}
	leftLog := make([]LogEntry, 0)
	startIndex := index
	for startIndex < rf.getLogLength() {
		rf.snapshotLog("snapshotting log...\n")
		leftLog = append(leftLog, rf.getLog(startIndex))
		startIndex += 1
	}
	// 非常隐蔽的问题！ rf.lastIncludedTerm = rf.getTermOfLog(index) 不能写在后面，因为这一步的getTermOfLog是依赖于 rf.lastIncludedIndex 的，提前更新了之后，每次都获取之前的 lastIncludedTerm，也就是永远会为 0
	// 但是，单纯换顺序也不对，因为 log 已经砍掉了，getTermOfLog 里也依赖 log, 这三者的更新顺序请注意
	rf.lastIncludedTerm = rf.getTermOfLog(index)
	rf.lastIncludedIndex = index
	rf.log = leftLog
	rf.snapshotLog("now leftLog: %v\n", rf.log)
	rf.persist()
	var data []byte
	rf.readPersist(data)
	rf.snapshotLog("start save state and snapshot\n")
	rf.persister.SaveStateAndSnapshot(data, snapshot)
	rf.snapshotLog("state and snapshot saved\n")
	rf.snapshotLog("finish snapshot\n")
}

type InstallSnapshotArgs struct {
	Term              int
	LeaderId          int
	LastIncludedIndex int
	LastIncludedTerm  int
	Data              []byte
	//Offset            int
	//Done              bool
}

type InstallSnapshotReply struct {
	Term int
}

//func (rf *Raft) InstallSnapshot(args *InstallSnapshotArgs, reply *InstallSnapshotReply) {
//
//}

//
// example RequestVote RPC arguments structure.
// field names must start with capital letters!
//
type RequestVoteArgs struct {
	// Your data here (2A, 2B).
	Term         int
	CandidateId  int
	LastLogIndex int
	LastLogTerm  int
}

//
// example RequestVote RPC reply structure.
// field names must start with capital letters!
//
type RequestVoteReply struct {
	// Your data here (2A).
	Term        int
	VoteGranted bool
}

//
// example RequestVote RPC handler.
//
func (rf *Raft) RequestVote(args *RequestVoteArgs, reply *RequestVoteReply) {
	// Your code here (2A, 2B).
	reply.VoteGranted = false
	rf.raftState.wLock()
	if args.Term >= rf.raftState.currentTerm {
		if args.Term > rf.raftState.currentTerm {
			rf.electionLog("update term and become follower if needed\n")
			rf.raftState.currentTerm = args.Term
			rf.raftState.votedFor = -1
			rf.raftState.state = followerState
			rf.logMutex.RLock()
			rf.persist()
			rf.logMutex.RUnlock()
		}
		if rf.raftState.votedFor == -1 || rf.raftState.votedFor == args.CandidateId {
			// if candidate's log is at least as up-to-date as mine, then grant vote
			// 怎么判断 candidate 是不是至少比 “我” 新？我有可能比它的 Log 少，而且我可能以前当过失败的 leader ，有着它没有的 Log，这个还挺有意思，可以在文档补充
			isNewerThanMe := false
			rf.logMutex.RLock()
			if rf.getLogLength() == 1 || args.LastLogTerm > rf.getTermOfLog(rf.getLogLength()-1) {
				isNewerThanMe = true
			} else if args.LastLogTerm == rf.getTermOfLog(rf.getLogLength()-1) {
				if args.LastLogIndex >= rf.getLogLength()-1 {
					isNewerThanMe = true
				}
			}
			rf.logMutex.RUnlock()
			if isNewerThanMe {
				rf.electionLog("vote for %d\n", args.CandidateId)
				reply.VoteGranted = true
				rf.raftState.votedFor = args.CandidateId
				rf.raftState.resetElectionTimer = true
				rf.logMutex.RLock()
				rf.persist()
				rf.logMutex.RUnlock()
			} else {
				rf.electionLog("refuse to vote for %d\n", args.CandidateId)
			}
		}
	}
	reply.Term = rf.raftState.currentTerm
	rf.raftState.wUnlock()
}

type AppendEntriesArgs struct {
	Term         int
	LeaderId     int
	Entries      []LogEntry
	PrevLogIndex int
	PrevLogTerm  int
	LeaderCommit int
}

type AppendEntriesReply struct {
	Term                int
	Success             bool
	NextRetryStartIndex int
}

// AppendEntries RPC handler
func (rf *Raft) AppendEntries(args *AppendEntriesArgs, reply *AppendEntriesReply) {
	if args.Entries == nil {
		rf.electionLog("recv heartbeat from %d\n", args.LeaderId)
	} else {
		rf.logReplicationLog("recv AppendEntries from %d\n", args.LeaderId)
	}
	rf.raftState.wLock()
	rf.logMutex.Lock()
	if args.Term > rf.raftState.currentTerm {
		rf.electionLog("update term and become follower if needed\n")
		rf.raftState.currentTerm = args.Term
		rf.raftState.state = followerState
		rf.raftState.votedFor = -1
		rf.persist()
	}
	if rf.raftState.state == candidateState && args.Term >= rf.raftState.currentTerm {
		rf.electionLog("turn into follower from candidate\n")
		rf.raftState.state = followerState
	}
	rf.raftState.resetElectionTimer = true

	reply.Term = rf.raftState.currentTerm
	reply.Success = true
	if args.Term < rf.raftState.currentTerm {
		rf.logReplicationLog("server %d 's term is old, return false\n", args.LeaderId)
		reply.Success = false
		rf.logMutex.Unlock()
		rf.raftState.wUnlock()
		return
	}
	if rf.lastIncludedIndex > args.PrevLogIndex {
		rf.snapshotLog("prevLogIndex smaller than snapshot lastIncludedIndex\n")
		reply.Success = false
		// 从日志+1开始，因为日志都是 applied 的，所以 leader 一定有
		reply.NextRetryStartIndex = rf.lastIncludedIndex + 1
		rf.logMutex.Unlock()
		rf.raftState.wUnlock()
		return
	}
	// 这里不能直接 log rf.getTermOfLog，有可能是非法的
	// rf.logReplicationLog("args.PrevLogIndex: %d, rf.getLogLength()-1: %d, rf.getTermOfLog(args.PrevLogIndex): %d, args.PrevLogTerm: %d\n", args.PrevLogIndex, rf.getLogLength()-1, rf.getTermOfLog(args.PrevLogIndex), args.PrevLogTerm)
	// 只有 applied 才会被 snapshot，所以其实访问到被 snapshot 过的数据并不是那么常见的事
	if args.PrevLogIndex > rf.getLogLength()-1 || (args.PrevLogIndex >= 0 && rf.getTermOfLog(args.PrevLogIndex) != args.PrevLogTerm) {
		rf.logReplicationLog("server %d 's prev log not match, return false\n", args.LeaderId)
		reply.Success = false
		if args.PrevLogIndex > rf.getLogLength()-1 {
			reply.NextRetryStartIndex = rf.getLogLength()
		} else {
			conflictTerm := rf.getTermOfLog(args.PrevLogIndex)
			tempIndex := args.PrevLogIndex
			if tempIndex == 0 {
				log.Fatalf("ERROR: log[0]'s term conflict???\n")
			}
			for tempIndex > 1 && rf.getTermOfLog(tempIndex) == conflictTerm {
				tempIndex -= 1
			}
			reply.NextRetryStartIndex = tempIndex
		}
		rf.logMutex.Unlock()
		rf.raftState.wUnlock()
		return
	}
	if args.Entries != nil {
		// 直接把新的都 append 进去
		// 这里原来不对，当并发比较高的时候，如果出现更长的 log 比更短的 log 的请求更早来，更短的 log 会把后面的给吞掉，更长的就没了
		rf.logReplicationLog("log copying...\n")
		rf.logReplicationLog("original log: %v\n", rf.log)
		for i := 0; i < len(args.Entries); i++ {
			if args.PrevLogIndex+1+i > rf.getLogLength()-1 {
				rf.log = append(rf.log, args.Entries[i])
			} else if rf.getTermOfLog(args.PrevLogIndex+1+i) != args.Entries[i].Term {
				rf.log = rf.log[0 : args.PrevLogIndex+1+i]
				rf.log = append(rf.log, args.Entries[i])
			}
		}
		//rf.log = rf.log[0 : args.PrevLogIndex+1]
		//rf.logReplicationLog("kept log: %v\n", rf.log)
		//for i := 0; i < len(args.Entries); i++ {
		//	rf.log = append(rf.log, args.Entries[i])
		//}
		rf.logReplicationLog("appended log: %v\n", rf.log)
		rf.persist()
	}
	rf.commitMutex.Lock()
	if args.LeaderCommit > rf.commitIndex {
		if args.LeaderCommit < rf.getLogLength()-1 {
			rf.commitIndex = args.LeaderCommit
		} else {
			rf.commitIndex = rf.getLogLength() - 1
		}
		rf.logReplicationLog("input 0 into applyNotifyCh\n")
		rf.applyNotifyCh <- 0
	}
	rf.commitMutex.Unlock()
	rf.logMutex.Unlock()
	rf.raftState.wUnlock()
}

//
// example code to send a RequestVote RPC to a server.
// server is the index of the target server in rf.peers[].
// expects RPC arguments in args.
// fills in *reply with RPC reply, so caller should
// pass &reply.
// the types of the args and reply passed to Call() must be
// the same as the types of the arguments declared in the
// handler function (including whether they are pointers).
//
// The labrpc package simulates a lossy network, in which servers
// may be unreachable, and in which requests and replies may be lost.
// Call() sends a request and waits for a reply. If a reply arrives
// within a timeout interval, Call() returns true; otherwise
// Call() returns false. Thus Call() may not return for a while.
// A false return can be caused by a dead server, a live server that
// can't be reached, a lost request, or a lost reply.
//
// Call() is guaranteed to return (perhaps after a delay) *except* if the
// handler function on the server side does not return.  Thus there
// is no need to implement your own timeouts around Call().
//
// look at the comments in ../labrpc/labrpc.go for more details.
//
// if you're having trouble getting RPC to work, check that you've
// capitalized all field names in structs passed over RPC, and
// that the caller passes the address of the reply struct with &, not
// the struct itself.
//
// func (rf *Raft) sendRequestVote(server int, args *RequestVoteArgs, reply *RequestVoteReply) bool {
// 	ok := rf.peers[server].Call("Raft.RequestVote", args, reply)
// 	return ok
// }

//
// the service using Raft (e.g. a k/v server) wants to start
// agreement on the next command to be appended to Raft's log. if this
// server isn't the leader, returns false. otherwise start the
// agreement and return immediately. there is no guarantee that this
// command will ever be committed to the Raft log, since the leader
// may fail or lose an election. even if the Raft instance has been killed,
// this function should return gracefully.
//
// the first return value is the index that the command will appear at
// if it's ever committed. the second return value is the current
// term. the third return value is true if this server believes it is
// the leader.
//
func (rf *Raft) Start(command interface{}) (int, int, bool) {
	rf.logReplicationLog("recv %v\n", command)
	index := -1
	term := -1
	isLeader := true

	// Your code here (2B).
	rf.raftState.rLock()
	isLeader = rf.raftState.isState(leaderState)
	if isLeader {
		//rf.logReplicationLog("waiting for leader init...\n")
		for {
			rf.leaderInitMutex.Lock()
			if rf.leaderInitDone {
				rf.leaderInitMutex.Unlock()
				break
			}
			rf.leaderInitMutex.Unlock()
			rf.logReplicationLog("waiting for leader init...\n")
			// 注意！这里有可能刚成为 leader ，甚至没去过 leaderMain，Start就来了，导致根本没机会init
			// 睡眠等待时释放锁，防死锁
			rf.raftState.rUnlock()
			time.Sleep(time.Millisecond * time.Duration(2))
			rf.raftState.rLock()
			if !rf.raftState.isState(leaderState) {
				rf.logReplicationLog("while waiting for leader init in Start, server role has changed\n")
				isLeader = false
				term = rf.raftState.currentTerm
				rf.logMutex.RLock()
				index = rf.getLogLength() - 1
				rf.logMutex.RUnlock()
				rf.raftState.rUnlock()
				return index, term, isLeader
			}
		}
		rf.logReplicationLog("append %v into local\n", command)
		rf.peerLogMutex.Lock()
		rf.logMutex.Lock()
		rf.logReplicationLog("original log: %v\n", rf.log)
		rf.log = append(rf.log, LogEntry{rf.raftState.currentTerm, command})
		rf.logReplicationLog("appended log: %v\n", rf.log)
		rf.persist()
		rf.matchIndex[rf.me] = rf.getLogLength() - 1
		rf.nextIndex[rf.me] = rf.getLogLength()
		index = rf.getLogLength() - 1
		rf.logMutex.Unlock()
		rf.peerLogMutex.Unlock()
		// broadcast this log
		// 这里直接 go 出去，这个 Start 需要 immediately return. (毕竟 rs 还锁着，请求的 reply 也可能改变 rs)
		// 这样有可能发生这样的情况：上一个 Start 加入了 log，还没 send 出去，新的 log 又加入了，然鹅仔细一看，这好像并不会引发错误
		rf.logReplicationLog("send out append entries\n")
		go rf.sendAppendEntriesToAll(rf.raftState.currentTerm)
	} else {
		rf.logMutex.RLock()
		index = rf.getLogLength() - 1
		rf.logMutex.RUnlock()
	}
	term = rf.raftState.currentTerm
	rf.raftState.rUnlock()
	return index, term, isLeader
}

//
// the tester doesn't halt goroutines created by Raft after each test,
// but it does call the Kill() method. your code can use killed() to
// check whether Kill() has been called. the use of atomic avoids the
// need for a lock.
//
// the issue is that long-running goroutines use memory and may chew
// up CPU time, perhaps causing later tests to fail and generating
// confusing debug output. any goroutine with a long-running loop
// should call killed() to check whether it should stop.
//
func (rf *Raft) Kill() {
	atomic.StoreInt32(&rf.dead, 1)
	// Your code here, if desired.
}

func (rf *Raft) killed() bool {
	z := atomic.LoadInt32(&rf.dead)
	return z == 1
}

// The ticker go routine starts a new election if this peer hasn't received
// heartsbeats recently.
// func (rf *Raft) ticker() {
// 	for rf.killed() == false {

// 		// Your code here to check if a leader election should
// 		// be started and to randomize sleeping time using
// 		// time.Sleep().

// 	}
// }

// 以 10 ms 为一个循环单位，循环单位太短可能会因为代码运行时间而影响实际时间
const loopTimeUnit = 10

type WrappedAppendEntriesReply struct {
	ok    bool
	from  int
	args  AppendEntriesArgs
	reply AppendEntriesReply
}

// 一些考虑：
// 因为 heartbeat 一直在发，如果每个都有 fail 而不结束（比如有一个 peer 挂了），
// 线程会堆积过多，导致不必要的内存占用
// 遂考虑心跳包失败不重发，只有增加日志重发
// 但是心跳的 reply 处理还是要做的，问题是怎么退出？答：计数，全都返回了或失败了就退出
// 这里的问题就是，如果上一条日志还没有 commit ，下一条命令就发过来了，raft 应该怎么做？

func (rf *Raft) sendHeartbeatToAll(thisTerm int) {
	replyChannel := make(chan WrappedAppendEntriesReply, 10)
	rf.logMutex.RLock()
	for i := 0; i < len(rf.peers); i++ {
		if i == rf.me {
			continue
		}
		prevLogIndex := rf.getLogLength() - 1
		prevLogTerm := rf.getTermOfLog(prevLogIndex)
		rf.commitMutex.RLock()
		args := AppendEntriesArgs{thisTerm, rf.me, nil, prevLogIndex, prevLogTerm, rf.commitIndex}
		rf.commitMutex.RUnlock()
		var reply AppendEntriesReply
		go rf.sendAppendEntriesWithChannelReply(i, &args, &reply, replyChannel)
	}
	rf.logMutex.RUnlock()
	// process reply
	replyCount := 0
	for {
		rf.raftState.rLock()
		if rf.raftState.currentTerm > thisTerm || rf.raftState.state != leaderState {
			rf.raftState.rUnlock()
			break
		}
		if replyCount == len(rf.peers)-1 {
			rf.raftState.rUnlock()
			break
		}
		rf.raftState.rUnlock()
		reply := <-replyChannel
		replyCount += 1
		if reply.ok {
			rf.electionLog("get heartbeat reply from server %d\n", reply.from)
			rf.raftState.wLock()
			rf.peerLogMutex.Lock()
			if reply.reply.Term > rf.raftState.currentTerm {
				rf.electionLog("turn into follower for term update\n")
				rf.raftState.currentTerm = reply.reply.Term
				rf.raftState.state = followerState
				rf.logMutex.RLock()
				rf.persist()
				rf.logMutex.RUnlock()
			}
			if reply.reply.Success {
				rf.logReplicationLog("server %d heartbeat success!\n", reply.from)
				// update nextIndex and matchIndex
				// 注意，这里有可能出现倒退的情况，因为并发度比较高时，消息的传回顺序完全可能是错乱的，我们需要保证这俩玩意儿是递增的，不然在有 snapshot 的情况下，可能就无意义地触及到已经变成快照的 Log 了
				// heartbeat 没有发送 entries，成功则说明 PrevLogIndex 处是匹配的
				if reply.args.PrevLogIndex > rf.matchIndex[reply.from] {
					rf.matchIndex[reply.from] = reply.args.PrevLogIndex
					rf.nextIndex[reply.from] = rf.matchIndex[reply.from] + 1
					rf.logReplicationLog("server %d 's matchIndex update to %d\n", reply.from, rf.matchIndex[reply.from])
					rf.logReplicationLog("server %d 's nextIndex update to %d\n", reply.from, rf.nextIndex[reply.from])
					// 更新 nextIndex 和 matchIndex 成功，尝试一下 commit 以及 apply
					go rf.tryIncrementCommitIndex()
				}
			}
			rf.peerLogMutex.Unlock()
			rf.raftState.wUnlock()
		}
	}
}

// TODO: 经常有 potential deadlock 和这里有关, 但这里的上锁顺序似乎没什么大毛病
// only for leader，followers' commitIndex is updated by AppendEntries.
func (rf *Raft) tryIncrementCommitIndex() {
	rf.logReplicationLog("try increment commit index\n")
	rf.raftState.rLock()
	rf.logMutex.RLock()
	rf.commitMutex.RLock()
	thisLogLength := rf.getLogLength()
	thisTerm := rf.raftState.currentTerm
	thisCommitIndex := rf.commitIndex
	rf.commitMutex.RUnlock()
	rf.logMutex.RUnlock()
	rf.raftState.rUnlock()
	rf.logReplicationLog("try increment commit release lock\n")
	for i := thisCommitIndex + 1; i < thisLogLength; i++ {
		rf.logMutex.RLock()
		nowLogTerm := rf.getTermOfLog(i)
		rf.logMutex.RUnlock()
		if nowLogTerm == thisTerm {
			count := 0
			rf.peerLogMutex.RLock()
			for j := 0; j < len(rf.peers); j++ {
				if rf.matchIndex[j] >= i {
					count += 1
				}
			}
			rf.peerLogMutex.RUnlock()
			rf.commitMutex.Lock()
			if count > len(rf.peers)/2 && i > rf.commitIndex {
				rf.commitIndex = i
				rf.logReplicationLog("most servers has the log %d, input 0 into applyNotifyCh\n", i)
				rf.applyNotifyCh <- 0
				rf.logReplicationLog("notify success\n")
			}
			rf.commitMutex.Unlock()
		}
	}
}

func (rf *Raft) sendAppendEntriesToAll(thisTerm int) {
	replyChannel := make(chan WrappedAppendEntriesReply, 10)
	rf.peerLogMutex.Lock()
	rf.logMutex.RLock()
	for i := 0; i < len(rf.peers); i++ {
		if i == rf.me {
			continue
		}
		rf.logReplicationLog("nextIndex[%d] = %d\n", i, rf.nextIndex[i])
		if rf.getLogLength()-1 >= rf.nextIndex[i] {
			// 这里的prevLogIndex 定义是 “index of log entry immediately preceding
			// new ones”，但我实际发送的是从 nextIndex 开始的 Entries，对那个 server 来说从这里开始都是 new ones，
			// 所以也应该让 prevLogIndex 为 nextIndex -1
			prevLogIndex := rf.nextIndex[i] - 1
			prevLogTerm := rf.getTermOfLog(prevLogIndex)
			rf.commitMutex.RLock()
			args := AppendEntriesArgs{thisTerm, rf.me, rf.getLogsFrom(rf.nextIndex[i]), prevLogIndex, prevLogTerm, rf.commitIndex}
			rf.commitMutex.RUnlock()
			var reply AppendEntriesReply
			go rf.sendAppendEntriesWithChannelReply(i, &args, &reply, replyChannel)
		}
	}
	rf.logMutex.RUnlock()
	rf.peerLogMutex.Unlock()
	// process reply
	successCount := 0
	for {
		// TODO: 这里的问题：无限重试什么时候停？
		if successCount == len(rf.peers)-1 {
			// all success
			break
		}
		// 这里等待中途，完全有可能发生状态的改变
		reply := <-replyChannel
		// 所以这里再判断一次，并且只有在提前判断&&锁住raftState的情况下，我们才能保证leaderTermSmaller这个bool的正确性
		rf.raftState.wLock()
		rf.peerLogMutex.Lock()
		if rf.raftState.currentTerm > thisTerm || rf.raftState.state != leaderState {
			rf.peerLogMutex.Unlock()
			rf.raftState.wUnlock()
			break
		}
		if reply.ok {
			rf.logReplicationLog("get AppendEntries reply from server %d\n", reply.from)
			leaderTermSmaller := false
			if reply.reply.Term > rf.raftState.currentTerm {
				leaderTermSmaller = true
				rf.electionLog("turn into follower for term update\n")
				rf.raftState.currentTerm = reply.reply.Term
				rf.raftState.state = followerState
				rf.logMutex.RLock()
				rf.persist()
				rf.logMutex.RUnlock()
			}
			if reply.reply.Success {
				rf.logReplicationLog("server %d append success!\n", reply.from)
				successCount += 1
				// update nextIndex and matchIndex
				// 注意，这里有可能出现倒退的情况，因为并发度比较高时，消息的传回顺序完全可能是错乱的，我们需要保证这俩玩意儿是递增的，不然在有 snapshot 的情况下，可能就无意义地触及到已经变成快照的 Log 了
				if reply.args.PrevLogIndex+len(reply.args.Entries) > rf.matchIndex[reply.from] {
					rf.matchIndex[reply.from] = reply.args.PrevLogIndex + len(reply.args.Entries)
					rf.nextIndex[reply.from] = rf.matchIndex[reply.from] + 1
					rf.logReplicationLog("server %d 's matchIndex update to %d\n", reply.from, rf.matchIndex[reply.from])
					rf.logReplicationLog("server %d 's nextIndex update to %d\n", reply.from, rf.nextIndex[reply.from])
				}
				// 每有成功的 AppendEntries 返回时，就尝试一下 commit 以及 apply
				go rf.tryIncrementCommitIndex()
			} else if !leaderTermSmaller {
				// 只有在 log inconsistent 时重发，term 问题不用重发
				rf.logReplicationLog("server %d append failed and retry!\n", reply.from)
				// decrement nextIndex and retry
				// 根据我自己的逻辑判断，prevLogIndex 也是要 -- 的
				//if rf.nextIndex[reply.from] > 1 {
				//	rf.nextIndex[reply.from] -= 1
				//}
				if reply.reply.NextRetryStartIndex == 0 {
					log.Fatalf("ERROR: NextRetryStartIndex is 0!!\n")
				}
				// TODO: 如果 reply.reply.NextRetryStartIndex 已经被包含进快照里，没有实际 log 了，那么把快照发给 follower
				rf.nextIndex[reply.from] = reply.reply.NextRetryStartIndex
				// 这里的重发并不使用最新的 term 和 leader commit，而是保持原请求的值，只更新prev、entries
				newArgs := reply.args
				// 将 PrevLogIndex 直接和 nextIndex 绑定而不是简单递减，避免多个 append to all 并发时出现问题
				newArgs.PrevLogIndex = rf.nextIndex[reply.from] - 1
				rf.logMutex.RLock()
				if newArgs.PrevLogIndex >= 0 {
					newArgs.PrevLogTerm = rf.getTermOfLog(newArgs.PrevLogIndex)
				}
				newArgs.Entries = rf.getLogsFrom(rf.nextIndex[reply.from])
				rf.logReplicationLog("newArgs | to: %v, prevLogIndex: %v, entries: %v\n", reply.from, newArgs.PrevLogIndex, newArgs.Entries)
				rf.logMutex.RUnlock()
				var newReply AppendEntriesReply
				go rf.sendAppendEntriesWithChannelReply(reply.from, &newArgs, &newReply, replyChannel)
			}
		} else {
			// resend same failed msg for network error
			rf.logReplicationLog("server %d append failed (for network error) and retry!\n", reply.from)
			var newReply AppendEntriesReply
			go rf.sendAppendEntriesWithChannelReply(reply.from, &reply.args, &newReply, replyChannel)
		}
		rf.peerLogMutex.Unlock()
		rf.raftState.wUnlock()
	}
}

func (rf *Raft) sendAppendEntriesWithChannelReply(to int, args *AppendEntriesArgs, reply *AppendEntriesReply, replyChannel chan WrappedAppendEntriesReply) {
	// 失败的也要传回 channel ，提供 retry 的契机
	ok := rf.peers[to].Call("Raft.AppendEntries", args, reply)
	wrappedReply := WrappedAppendEntriesReply{ok, to, *args, *reply}
	replyChannel <- wrappedReply
}

func (rf *Raft) leaderMain() {
	// 发现有这里还没 init 完，已经有请求 Start 进了这个 leader ，导致 nextIndex 等 state 错误
	// 添加 leaderInitDone 在 RaftState 中
	// reinitialize nextIndex and matchIndex
	rf.peerLogMutex.Lock()
	rf.nextIndex = make([]int, len(rf.peers))
	rf.matchIndex = make([]int, len(rf.peers))
	rf.logMutex.RLock()
	for i, _ := range rf.nextIndex {
		rf.nextIndex[i] = rf.getLogLength()
	}
	for i, _ := range rf.matchIndex {
		rf.matchIndex[i] = 0
	}
	rf.logMutex.RUnlock()
	rf.peerLogMutex.Unlock()
	rf.leaderInitMutex.Lock()
	rf.leaderInitDone = true
	rf.leaderInitMutex.Unlock()
	rf.logReplicationLog("leader init done\n")
	// 每 40 ms 发送一轮 heartbeat
	for {
		// send heartbeat to all servers
		rf.electionLog("send out heartbeats\n")
		rf.raftState.rLock()
		go rf.sendHeartbeatToAll(rf.raftState.currentTerm)
		rf.raftState.rUnlock()

		time.Sleep(time.Millisecond * time.Duration(loopTimeUnit*4))

		// check if state changed by reply
		rf.raftState.rLock()
		if !rf.raftState.isState(leaderState) {
			rf.raftState.rUnlock()
			break
		}
		rf.raftState.rUnlock()
	}
}

// warpped reply for check and retry
type WrappedRequestVoteReply struct {
	ok    bool
	from  int
	args  RequestVoteArgs
	reply RequestVoteReply
}

func (rf *Raft) sendRequestVoteToAll(thisTerm int) {
	grantedVoteSum := 1
	replyChannel := make(chan WrappedRequestVoteReply, 10)
	for i := 0; i < len(rf.peers); i++ {
		if i == rf.me {
			continue
		}
		rf.logMutex.RLock()
		args := RequestVoteArgs{thisTerm, rf.me, rf.getLogLength() - 1, rf.getTermOfLog(rf.getLogLength() - 1)}
		rf.logMutex.RUnlock()
		var reply RequestVoteReply
		go rf.sendRequestVoteWithChannelReply(i, &args, &reply, replyChannel)
	}
	// process reply
	for {
		reply := <-replyChannel
		rf.raftState.wLock()
		if rf.raftState.currentTerm > thisTerm || rf.raftState.state != candidateState {
			rf.raftState.wUnlock()
			break
		}
		if reply.ok {
			rf.electionLog("get reply from server %d\n", reply.from)
			if reply.reply.Term > rf.raftState.currentTerm {
				rf.electionLog("turn into follower for term update\n")
				rf.raftState.currentTerm = reply.reply.Term
				rf.raftState.state = followerState
				rf.logMutex.RLock()
				rf.persist()
				rf.logMutex.RUnlock()
			}
			if reply.reply.VoteGranted {
				grantedVoteSum += 1
				if grantedVoteSum > len(rf.peers)/2 && rf.raftState.isState(candidateState) {
					rf.electionLog("become leader\n")
					rf.raftState.state = leaderState
					rf.leaderInitMutex.Lock()
					rf.leaderInitDone = false
					rf.leaderInitMutex.Unlock()
				}
			}
		} else {
			// retry
			var retryReply RequestVoteReply
			go rf.sendRequestVoteWithChannelReply(reply.from, &reply.args, &retryReply, replyChannel)
		}
		rf.raftState.wUnlock()
	}
}

func (rf *Raft) sendRequestVoteWithChannelReply(to int, args *RequestVoteArgs, reply *RequestVoteReply, replyChannel chan WrappedRequestVoteReply) {
	// 失败的也要传回 channel ，提供 retry 的契机
	ok := rf.peers[to].Call("Raft.RequestVote", args, reply)
	wrappedReply := WrappedRequestVoteReply{ok, to, *args, *reply}
	replyChannel <- wrappedReply
}

func (rf *Raft) doElection() {
	rf.raftState.wLock()
	rf.raftState.currentTerm += 1
	rf.raftState.votedFor = rf.me
	rf.logMutex.RLock()
	rf.persist()
	rf.logMutex.RUnlock()
	// send out request vote rpc
	go rf.sendRequestVoteToAll(rf.raftState.currentTerm)
	rf.raftState.wUnlock()
	var electionMaxTime int
	electionMaxTime = 15 + rand.Intn(15)
	for i := 0; i < electionMaxTime; i++ {
		time.Sleep(time.Duration(loopTimeUnit) * time.Millisecond)
		rf.raftState.rLock()
		// 如果发现已经不是 candidate，则 break
		if rf.raftState.state != candidateState {
			rf.raftState.rUnlock()
			break
		}
		rf.raftState.rUnlock()
	}
	// next election time out or change to other state
}

func (rf *Raft) followerMain() {
	var electionMaxTime int
	electionMaxTime = 15 + rand.Intn(15)
	for i := 0; i < electionMaxTime; i++ {
		time.Sleep(time.Duration(loopTimeUnit) * time.Millisecond)
		rf.raftState.rLock()
		if rf.raftState.resetElectionTimer {
			i = 0
			rf.raftState.resetElectionTimer = false
		}
		rf.raftState.rUnlock()
	}
	rf.raftState.wLock()
	rf.raftState.state = candidateState
	rf.electionLog("election time out,  become candidate\n")
	rf.raftState.wUnlock()
}

func (rf *Raft) mainLoop() {
	for !rf.killed() {
		// atomically read state
		rf.raftState.rLock()
		curState := rf.raftState.state
		rf.raftState.rUnlock()
		switch curState {
		case leaderState:
			rf.leaderMain()
			break
		case candidateState:
			rf.doElection()
			break
		case followerState:
			rf.followerMain()
			break
		default:
			log.Fatalln("undefined raft state.")
			break
		}
	}
}

func (rf *Raft) applyLoop() {
	for !rf.killed() {
		// waiting for notify
		_ = <-rf.applyNotifyCh
		rf.logReplicationLog("applyNotifyCh recv notification\n")
		needUpdate := false
		rf.commitMutex.RLock()
		if rf.commitIndex > rf.lastApplied {
			needUpdate = true
		}
		rf.commitMutex.RUnlock()
		rf.logReplicationLog("needUpdate: %v\n", needUpdate)
		if needUpdate {
			nextApplied := rf.lastApplied + 1
			rf.logMutex.RLock()
			rf.commitMutex.RLock()
			toBeApplied := rf.getLogs(nextApplied, rf.commitIndex+1)
			rf.commitMutex.RUnlock()
			rf.logMutex.RUnlock()
			rf.logReplicationLog("to be applied (from index %v): %v\n", nextApplied, toBeApplied)
			// send to channel
			// 注意！！！这里有可能会乱 go，导致顺序错误
			//go func(nextApplied int, toBeApplied *[]LogEntry) {
			//	for i, entry := range *toBeApplied {
			//		rf.applyCh <- ApplyMsg{
			//			Command:      entry.Command,
			//			CommandValid: true,
			//			CommandIndex: i + nextApplied,
			//		}
			//	}
			//}(nextApplied, &toBeApplied)
			for i, entry := range toBeApplied {
				rf.applyCh <- ApplyMsg{
					Command:      entry.Command,
					CommandValid: true,
					CommandIndex: i + nextApplied,
				}
				rf.logReplicationLog("apply log %d success\n", i+nextApplied)
			}
			rf.logReplicationLog("apply %d entries (from index %v) to state machine\n", len(toBeApplied), nextApplied)
			rf.lastApplied += len(toBeApplied)
			rf.logReplicationLog("latest lastApplied: %d\n", rf.lastApplied)
		}
	}
}

//
// the service or tester wants to create a Raft server. the ports
// of all the Raft servers (including this one) are in peers[]. this
// server's port is peers[me]. all the servers' peers[] arrays
// have the same order. persister is a place for this server to
// save its persistent state, and also initially holds the most
// recent saved state, if any. applyCh is a channel on which the
// tester or service expects Raft to send ApplyMsg messages.
// Make() must return quickly, so it should start goroutines
// for any long-running work.
//
func Make(peers []*labrpc.ClientEnd, me int,
	persister *Persister, applyCh chan ApplyMsg) *Raft {
	rf := &Raft{}
	rf.peers = peers
	rf.persister = persister
	rf.me = me

	// Your initialization code here (2A, 2B, 2C).
	//rf.electionDebug = true
	//rf.logReplicationDebug = true
	//rf.persistenceDebug = true
	//rf.snapshotDebug = true
	rf.electionDebug = false
	rf.logReplicationDebug = false
	rf.persistenceDebug = false
	rf.snapshotDebug = false
	rf.raftState = MakeRaftState(rf)
	// log[0] is unused, just to start at 1.
	rf.log = make([]LogEntry, 1)
	rf.log[0] = LogEntry{
		Term:    0,
		Command: nil,
	}
	rf.lastIncludedIndex = 0
	rf.lastIncludedTerm = 0
	rf.commitIndex = 0
	rf.lastApplied = 0
	rf.applyCh = applyCh
	rf.leaderInitDone = false
	rf.applyNotifyCh = make(chan int, 10)

	// initialize from state persisted before a crash
	rf.persistenceLog("remake and readPersist\n")
	rf.readPersist(persister.ReadRaftState())

	if rf.lastIncludedIndex > 0 {
		rf.commitIndex = rf.lastIncludedIndex
		rf.lastApplied = rf.lastIncludedIndex
	}

	seed := time.Now().UnixNano()
	rand.Seed(seed)
	rf.electionLog("rand seed: %v\n", seed)

	// start ticker goroutine to start elections
	// go rf.ticker()
	go rf.mainLoop()
	go rf.applyLoop()
	return rf
}
