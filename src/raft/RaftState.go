package raft

import (
	"github.com/sasha-s/go-deadlock"
)

const candidateState int = 0
const leaderState int = 1
const followerState int = 2

type RaftState struct {
	state       int // state of this server, Candidate, Leader, Follower
	currentTerm int
	votedFor    int

	rf *Raft

	//rwmutex sync.RWMutex
	rwmutex deadlock.RWMutex
}

func (rs *RaftState) getState() int {
	return rs.state
}

func (rs *RaftState) isState(state int) bool {
	return rs.state == state
}

func (rs *RaftState) rLock() {
	rs.rwmutex.RLock()
}

func (rs *RaftState) rUnlock() {
	rs.rwmutex.RUnlock()
}

func (rs *RaftState) wLock() {
	rs.rwmutex.Lock()
}

func (rs *RaftState) wUnlock() {
	rs.rwmutex.Unlock()
}
