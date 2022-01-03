package raft

import (
	"bytes"
	"encoding/gob"
	"labrpc"
	"math/rand"
	"sync"
	"time"
)

const (
	FOLLOWER  = 0
	CANDIDATE = 1
	LEADER    = 2
)

const (
	electionTimeoutMin = 300 * time.Millisecond
	electionTimeoutMax = 600 * time.Millisecond
	heartbeatTimeout   = 50 * time.Millisecond
	checkTimeout       = 5 * time.Millisecond
)

func randTime() time.Duration {
	diff := (electionTimeoutMax - electionTimeoutMin).Milliseconds()
	return electionTimeoutMin + time.Duration(rand.Intn(int(diff)))*time.Millisecond
}

func wait(n int, ch chan bool) {
	for i := 1; i < n; i++ {
		select {
		case <-ch:
		case <-time.After(checkTimeout):
			return
		}
	}
}

type ApplyMsg struct {
	Index       int
	Command     interface{}
	UseSnapshot bool
	Snapshot    []byte
}

type LogItem struct {
	Term    int
	Command interface{}
}

type Raft struct {
	mu        sync.Mutex
	peers     []*labrpc.ClientEnd
	persister *Persister
	me        int

	// 修改以下三条必须持久化
	currentTerm int
	votedFor    int
	log         []LogItem

	commitIndex int
	lastApplied int

	nextIndex  []int
	matchIndex []int

	role  int
	votes int
	timer *time.Timer
}

func (rf *Raft) GetState() (int, bool) {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	term := rf.currentTerm
	isLeader := rf.role == LEADER
	return term, isLeader
}

func (rf *Raft) persist() {
	w := new(bytes.Buffer)
	e := gob.NewEncoder(w)
	e.Encode(rf.currentTerm)
	e.Encode(rf.votedFor)
	e.Encode(rf.log)
	data := w.Bytes()
	rf.persister.SaveRaftState(data)
}

func (rf *Raft) readPersist(data []byte) {
	r := bytes.NewBuffer(data)
	d := gob.NewDecoder(r)
	d.Decode(&rf.currentTerm)
	d.Decode(&rf.votedFor)
	d.Decode(&rf.log)
}

func (rf *Raft) keepOrFollow(term int) {
	if term > rf.currentTerm {
		rf.currentTerm = term
		rf.votedFor = -1 // 重置投票
		rf.persist()
		rf.role = FOLLOWER
	}
}

type RequestVoteArgs struct {
	Term         int
	CandidateId  int
	LastLogIndex int
	LastLogTerm  int
}

type RequestVoteReply struct {
	Term        int
	VoteGranted bool
}

func (rf *Raft) RequestVote(args RequestVoteArgs, reply *RequestVoteReply) {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	reply.Term = rf.currentTerm
	rf.keepOrFollow(args.Term)
	if args.Term >= rf.currentTerm &&
		(rf.votedFor == -1 || rf.votedFor == args.CandidateId) &&
		(args.LastLogTerm > rf.log[len(rf.log)-1].Term ||
			args.LastLogTerm == rf.log[len(rf.log)-1].Term &&
				args.LastLogIndex >= len(rf.log)-1) { // 日志至少一样新
		rf.votedFor = args.CandidateId
		rf.persist()
		rf.timer.Reset(randTime()) // 重置选举计时
		reply.VoteGranted = true
	}
}

func (rf *Raft) sendRequestVote(server int, args RequestVoteArgs, reply *RequestVoteReply, ch chan bool) {
	if !rf.peers[server].Call("Raft.RequestVote", args, reply) {
		return
	}
	rf.mu.Lock()
	rf.keepOrFollow(reply.Term)
	if rf.role == CANDIDATE && reply.VoteGranted {
		rf.votes += 1
	}
	rf.mu.Unlock()
	ch <- true
}

type AppendEntriesArgs struct {
	Term         int
	LeaderId     int
	PrevLogIndex int
	PrevLogTerm  int
	Entries      []LogItem
	LeaderCommit int
}

type AppendEntriesReply struct {
	Term    int
	Success bool
}

func (rf *Raft) AppendEntries(args AppendEntriesArgs, reply *AppendEntriesReply) {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	reply.Term = rf.currentTerm
	if args.Term == rf.currentTerm {
		rf.role = FOLLOWER
		rf.timer.Reset(randTime()) // term相同重置计时
	}
	rf.keepOrFollow(args.Term)
	if args.Term >= rf.currentTerm &&
		args.PrevLogIndex < len(rf.log) &&
		args.PrevLogTerm == rf.log[args.PrevLogIndex].Term { // term和log要匹配
		if args.PrevLogIndex+1 != len(rf.log) || args.Entries != nil {
			rf.log = append(rf.log[:args.PrevLogIndex+1], args.Entries...) // 删除不匹配并添加未持有的日志
			rf.persist()
		}
		if args.LeaderCommit > rf.commitIndex {
			if args.LeaderCommit < len(rf.log) {
				rf.commitIndex = args.LeaderCommit
			} else {
				rf.commitIndex = len(rf.log)
			}
		}
		reply.Success = true
	}
}

func (rf *Raft) sendAppendEntries(server int, args AppendEntriesArgs, reply *AppendEntriesReply, ch chan bool) {
	if !rf.peers[server].Call("Raft.AppendEntries", args, reply) {
		return
	}
	rf.mu.Lock()
	rf.keepOrFollow(reply.Term)
	if rf.role == LEADER && reply.Success {
		rf.nextIndex[server] = args.PrevLogIndex + len(args.Entries) + 1
		rf.matchIndex[server] = args.PrevLogIndex + len(args.Entries)
	} else if rf.role == LEADER && !reply.Success {
		rf.nextIndex[server]--
		for rf.nextIndex[server] >= 0 && rf.log[rf.nextIndex[server]].Term == reply.Term {
			rf.nextIndex[server]-- // 跳过同一term的log
		}
	}
	rf.mu.Unlock()
	ch <- true
}

func (rf *Raft) Start(command interface{}) (int, int, bool) {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	index := len(rf.log)
	term := rf.currentTerm
	isLeader := rf.role == LEADER
	if isLeader {
		rf.log = append(rf.log, LogItem{term, command})
		rf.persist()
	}
	return index, term, isLeader
}

func (rf *Raft) Kill() {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	rf.me = -1
	rf.timer.Reset(0)
}

func Make(peers []*labrpc.ClientEnd, me int,
	persister *Persister, applyCh chan ApplyMsg) *Raft {
	n := len(peers)
	rf := &Raft{}
	rf.peers = peers
	rf.persister = persister
	rf.me = me
	rf.votedFor = -1
	rf.log = make([]LogItem, 1)
	rf.nextIndex = make([]int, n)
	rf.matchIndex = make([]int, n)
	rf.role = FOLLOWER
	rf.timer = time.NewTimer(randTime())
	rf.readPersist(persister.ReadRaftState())

	go func() {
		for rf.me != -1 {
			time.Sleep(checkTimeout)
			rf.mu.Lock()
			for rf.me != -1 && rf.commitIndex > rf.lastApplied {
				rf.lastApplied++
				applyCh <- ApplyMsg{Index: rf.lastApplied, Command: rf.log[rf.lastApplied].Command}
			}
			rf.mu.Unlock()
		}
	}()

	go func() {
		for rf.me != -1 {
			<-rf.timer.C
			switch rf.role {
			case FOLLOWER:
				rf.mu.Lock()
				rf.role = CANDIDATE
				rf.timer.Reset(0)
				rf.mu.Unlock()

			case CANDIDATE:
				rf.mu.Lock()
				rf.currentTerm++
				rf.votedFor = me
				rf.persist()
				rf.votes = 1
				rf.timer.Reset(randTime())
				m := len(rf.log)
				rf.mu.Unlock()

				ch := make(chan bool)
				for _, i := range rand.Perm(n) {
					rf.mu.Lock()
					if rf.me != -1 && rf.role == CANDIDATE && i != rf.me {
						args := RequestVoteArgs{rf.currentTerm, rf.me,
							m - 1, rf.log[m-1].Term}
						reply := RequestVoteReply{}
						go rf.sendRequestVote(i, args, &reply, ch)
					}
					rf.mu.Unlock()
				}

				wait(n, ch) // 等待所有发收成功或超时

				rf.mu.Lock()
				if rf.me != -1 && rf.role == CANDIDATE && 2*rf.votes > n { // 成功竞选
					rf.role = LEADER
					rf.timer.Reset(0)
					for i := 0; i < n; i++ {
						rf.nextIndex[i] = m
					}
				}
				rf.mu.Unlock()

			case LEADER:
				rf.mu.Lock()
				rf.timer.Reset(heartbeatTimeout)
				m := len(rf.log)
				rf.mu.Unlock()

				ch := make(chan bool)
				for _, i := range rand.Perm(n) {
					rf.mu.Lock()
					if rf.me != -1 && rf.role == LEADER && i != rf.me {
						args := AppendEntriesArgs{rf.currentTerm, rf.me,
							rf.nextIndex[i] - 1, rf.log[rf.nextIndex[i]-1].Term,
							nil, rf.commitIndex}
						if rf.nextIndex[i] < m {
							args.Entries = make([]LogItem, m-rf.nextIndex[i])
							copy(args.Entries, rf.log[rf.nextIndex[i]:m])
						}
						reply := AppendEntriesReply{}
						go rf.sendAppendEntries(i, args, &reply, ch)
					}
					rf.mu.Unlock()
				}

				wait(n, ch) // 等待所有发收成功或超时

				rf.mu.Lock()
				N := m - 1
				if rf.me != -1 && rf.role == LEADER && N > rf.commitIndex && rf.log[N].Term == rf.currentTerm {
					count := 1
					for i := 0; i < n; i++ {
						if i != me && rf.matchIndex[i] >= N {
							count++
						}
					}
					if 2*count > n { // 确认提交
						rf.commitIndex = N
					}
				}
				rf.mu.Unlock()
			}
		}
	}()
	return rf
}
