// Copyright 2015 The etcd Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package raft

import (
	"errors"
	"github.com/gogo/protobuf/sortkeys"
	"github.com/pingcap-incubator/tinykv/log"
	"math/rand"

	pb "github.com/pingcap-incubator/tinykv/proto/pkg/eraftpb"
)

// None is a placeholder node ID used when there is no leader.
const None uint64 = 0

// StateType represents the role of a node in a cluster.
type StateType uint64

const (
	StateFollower StateType = iota
	StateCandidate
	StateLeader
)

var stmap = [...]string{
	"StateFollower",
	"StateCandidate",
	"StateLeader",
}

func (st StateType) String() string {
	return stmap[uint64(st)]
}

// ErrProposalDropped is returned when the proposal is ignored by some cases,
// so that the proposer can be notified and fail fast.
var ErrProposalDropped = errors.New("raft proposal dropped")

// Config contains the parameters to start a raft.
type Config struct {
	// ID is the identity of the local raft. ID cannot be 0.
	ID uint64

	// peers contains the IDs of all nodes (including self) in the raft cluster. It
	// should only be set when starting a new raft cluster. Restarting raft from
	// previous configuration will panic if peers is set. peer is private and only
	// used for testing right now.
	peers []uint64

	// ElectionTick is the number of Node.Tick invocations that must pass between
	// elections. That is, if a follower does not receive any message from the
	// leader of current term before ElectionTick has elapsed, it will become
	// candidate and start an election. ElectionTick must be greater than
	// HeartbeatTick. We suggest ElectionTick = 10 * HeartbeatTick to avoid
	// unnecessary leader switching.
	ElectionTick int
	// HeartbeatTick is the number of Node.Tick invocations that must pass between
	// heartbeats. That is, a leader sends heartbeat messages to maintain its
	// leadership every HeartbeatTick ticks.
	HeartbeatTick int

	// Storage is the storage for raft. raft generates entries and states to be
	// stored in storage. raft reads the persisted entries and states out of
	// Storage when it needs. raft reads out the previous state and configuration
	// out of storage when restarting.
	Storage Storage
	// Applied is the last applied index. It should only be set when restarting
	// raft. raft will not return entries to the application smaller or equal to
	// Applied. If Applied is unset when restarting, raft might return previous
	// applied entries. This is a very application dependent configuration.
	Applied uint64
}

func (c *Config) validate() error {
	if c.ID == None {
		return errors.New("cannot use none as id")
	}

	if c.HeartbeatTick <= 0 {
		return errors.New("heartbeat tick must be greater than 0")
	}

	if c.ElectionTick <= c.HeartbeatTick {
		return errors.New("election tick must be greater than heartbeat tick")
	}

	if c.Storage == nil {
		return errors.New("storage cannot be nil")
	}

	return nil
}

// Progress represents a follower’s progress in the view of the leader. Leader maintains
// progresses of all followers, and sends entries to the follower based on its progress.
type Progress struct {
	Match, Next uint64
}

type Raft struct {
	id uint64

	Term uint64
	Vote uint64

	// the log
	RaftLog *RaftLog

	// log replication progress of each peers
	Prs map[uint64]*Progress

	// this peer's role
	State StateType

	// votes records
	votes map[uint64]bool

	// msgs need to send
	msgs []pb.Message

	// the leader id
	Lead uint64

	// heartbeat interval, should send
	heartbeatTimeout int
	// baseline of election interval
	electionTimeout int
	// a random num in range [electionTimeout, 2 * electionTimeout - 1]
	// to break the tie in election
	randomElectionTimeout int
	// number of ticks since it reached last heartbeatTimeout.
	// only leader keeps heartbeatElapsed.
	heartbeatElapsed int
	// number of ticks since it reached last electionTimeout
	electionElapsed int

	// leadTransferee is id of the leader transfer target when its value is not zero.
	// Follow the procedure defined in section 3.10 of Raft phd thesis.
	// (https://web.stanford.edu/~ouster/cgi-bin/papers/OngaroPhD.pdf)
	// (Used in 3A leader transfer)
	leadTransferee uint64

	// Only one conf change may be pending (in the log, but not yet
	// applied) at a time. This is enforced via PendingConfIndex, which
	// is set to a value >= the log index of the latest pending
	// configuration change (if any). Config changes are only allowed to
	// be proposed if the leader's applied index is greater than this
	// value.
	// (Used in 3A conf change)
	PendingConfIndex uint64

	stepFunc func(m pb.Message) error
	tickFunc func()
}

// newRaft return a raft peer with the given config
func newRaft(c *Config) *Raft {
	if err := c.validate(); err != nil {
		panic(err.Error())
	}
	// Your Code Here (2A).
	prs := make(map[uint64]*Progress)
	for i := 1; i <= len(c.peers); i++ {
		prs[uint64(i)] = nil
	}
	raft := &Raft{
		id:               c.ID,
		heartbeatTimeout: c.HeartbeatTick,
		electionTimeout:  c.ElectionTick,
		RaftLog:          newLog(c.Storage),
		Prs:              prs,
	}
	raft.becomeFollower(0, None)
	return raft
}

func (r *Raft) send(m pb.Message) {
	r.msgs = append(r.msgs, m)
}

// sendAppend sends an append RPC with new entries (if any) and the
// current commit index to the given peer. Returns true if a message was sent.
func (r *Raft) sendAppend(to uint64) bool {
	// Your Code Here (2A).
	li := r.RaftLog.LastIndex()
	ents := make([]*pb.Entry, 0)
	var logTerm, idx uint64
	if li < r.Prs[to].Next {
		return false
		//noop := pb.Entry{
		//	EntryType: pb.EntryType_EntryNormal,
		//	Term:      r.RaftLog.LastTerm(),
		//	Index:     li,
		//	Data:      nil,
		//}
		//logTerm = noop.Term
		//idx = li
		//ents = append(ents, &noop)
	} else {
		entries, err := r.RaftLog.GetEntries(r.Prs[to].Next)
		if err != nil {
			panic(err)
		}
		idx = r.Prs[to].Next - 1
		logTerm, err = r.RaftLog.Term(idx)
		if err != nil {
			panic(err)
		}
		for _, v := range entries {
			ents = append(ents, &v)
		}
		log.Infof("Node %d prepare AppendMsg for Node %d: %v %v (start with idx %d)",
			r.id, to, ents, entries, r.Prs[to].Next)
	}
	m := pb.Message{
		MsgType: pb.MessageType_MsgAppend,
		To:      to,
		From:    r.id,
		Term:    r.Term,
		LogTerm: logTerm,
		Index:   idx,
		Entries: ents,
		Commit:  r.RaftLog.committed,
	}
	r.send(m)
	return true
}

// sendHeartbeat sends a heartbeat RPC to the given peer.
func (r *Raft) sendHeartbeat(to uint64) {
	// Your Code Here (2A).
	m := pb.Message{
		MsgType: pb.MessageType_MsgHeartbeat,
		To:      to,
		From:    r.id,
		Term:    r.Term,
	}
	r.send(m)
}

// sendHeartbeat sends a heartbeat RPC to the given peer.
func (r *Raft) sendRequestVote(to uint64) {
	m := pb.Message{
		MsgType: pb.MessageType_MsgRequestVote,
		To:      to,
		From:    r.id,
		Term:    r.Term,
		LogTerm: r.RaftLog.LastTerm(),
	}
	r.send(m)
}

func (r *Raft) sendRequestVoteResponse(to uint64, rejected bool) {
	m := pb.Message{
		MsgType: pb.MessageType_MsgRequestVoteResponse,
		To:      to,
		From:    r.id,
		Term:    r.Term,
		Reject:  rejected,
	}
	r.send(m)
}

// tick advances the internal logical clock by a single tick.
func (r *Raft) tick() {
	// Your Code Here (2A).
	r.tickFunc()
}

// tickElection is the logical clock for Follower and Candidate.
func (r *Raft) tickElection() {
	r.electionElapsed++
	if r.electionElapsed >= r.randomElectionTimeout {
		r.electionElapsed = 0
		log.Infof("Node %d election timeout.", r.id)
		// send local message `MessageType_MsgHup` to become Candidate and start a election
		r.Step(pb.Message{MsgType: pb.MessageType_MsgHup, From: r.id})
	}
}

// tickHeartbeat is the logical clock for Leader.
func (r *Raft) tickHeartbeat() {
	r.heartbeatElapsed++
	if r.heartbeatElapsed >= r.heartbeatTimeout {
		r.heartbeatElapsed = 0
		log.Infof("Node %d heartbeat timeout.", r.id)
		// send local message `MessageType_MsgBeat` to notify Leader.
		r.Step(pb.Message{MsgType: pb.MessageType_MsgBeat, From: r.id})
	}
}

// reset Term/Vote/Lead and recalculate election timeout.
func (r *Raft) reset(term uint64) {
	if r.Term != term {
		r.Term = term
		r.Vote = None
	}
	r.Lead = None
	r.electionElapsed = 0
	r.heartbeatElapsed = 0
	r.randomElectionTimeout = r.electionTimeout + rand.Intn(r.electionTimeout)

	r.votes = make(map[uint64]bool, 0)
}

// becomeFollower transform this peer's state to Follower
func (r *Raft) becomeFollower(term uint64, lead uint64) {
	// Your Code Here (2A).
	r.reset(term)
	r.State = StateFollower
	r.tickFunc = r.tickElection
	r.stepFunc = r.stepFollower

	r.Lead = lead
	log.Infof("Node %d become follower at term %d", r.id, r.Term)
}

// becomeCandidate transform this peer's state to candidate
func (r *Raft) becomeCandidate() {
	// Your Code Here (2A).
	r.reset(r.Term + 1)
	r.State = StateCandidate
	r.tickFunc = r.tickElection
	r.stepFunc = r.stepCandidate

	r.Vote = r.id
	r.votes[r.id] = true
	log.Infof("Node %d become candidate at term %d", r.id, r.Term)

	if len(r.Prs) <= 1 {
		r.becomeLeader()
	}
}

// becomeLeader transform this peer's state to leader
func (r *Raft) becomeLeader() {
	// Your Code Here (2A).
	// NOTE: Leader should propose a noop entry on its term
	r.reset(r.Term)
	r.State = StateLeader
	r.tickFunc = r.tickHeartbeat
	r.stepFunc = r.stepLeader

	r.Lead = r.id
	for i := range r.Prs {
		if i == r.id {
			r.Prs[i] = nil
		} else {
			r.Prs[i] = &Progress{Next: r.RaftLog.LastIndex() + 1, Match: 0}
		}
	}
	// append a noop entry on its term
	//r.appendEntries([]*pb.Entry{
	//	{
	//		EntryType: pb.EntryType_EntryNormal,
	//		Data:      nil,
	//	},
	//}...)
	log.Infof("Node %d become leader at term %d", r.id, r.Term)
}

// In order to pass TestRecvMessageType_MsgRequestVote2AA.
func (r *Raft) makeCheckerHappy() {
	switch r.State {
	case StateFollower:
		r.tickFunc = r.tickElection
		r.stepFunc = r.stepFollower
	case StateCandidate:
		r.tickFunc = r.tickElection
		r.stepFunc = r.stepCandidate
	case StateLeader:
		r.tickFunc = r.tickHeartbeat
		r.stepFunc = r.stepLeader
	}
}

// Step the entrance of handle message, see `MessageType`
// on `eraftpb.proto` for what msgs should be handled
func (r *Raft) Step(m pb.Message) error {
	// Your Code Here (2A).
	r.makeCheckerHappy()
	switch {
	case m.Term == 0:
		// local message
	case m.Term > r.Term:
		log.Infof("Node %d (Term: %d) received a message with greater Term %d", r.id, r.Term, m.Term)
		if m.MsgType == pb.MessageType_MsgAppend || m.MsgType == pb.MessageType_MsgHeartbeat ||
			m.MsgType == pb.MessageType_MsgSnapshot {
			r.becomeFollower(m.Term, m.From)
		} else {
			r.becomeFollower(m.Term, None)
		}
	case m.Term < r.Term:
		// ignore this message
		log.Infof("Node %d (Term: %d) ignored a message with Term: %d", r.id, r.Term, m.Term)
		rep := pb.Message{
			To:   m.From,
			From: r.id,
			Term: r.Term,
		}
		switch m.MsgType {
		case pb.MessageType_MsgRequestVote:
			rep.MsgType = pb.MessageType_MsgRequestVoteResponse
			rep.Reject = true
			r.send(rep)
		case pb.MessageType_MsgHeartbeat:
			// HeartbeatResponse is only used here to notify an old leader.
			rep.MsgType = pb.MessageType_MsgHeartbeatResponse
			r.send(rep)
		}
		return nil
	}
	switch m.MsgType {
	// the nodes of three states have the same code of handling RequestVote.
	case pb.MessageType_MsgRequestVote:
		if m.Term != r.Term {
			log.Panicf("Node %d get RequestVote with different term: want %d, get %d",
				r.id, r.Term, m.Term)
		}
		r.handleRequestVote(m)
	default:
		// Now it's ok that raft node only handles the messages which m.Term == r.Term or local message.
		return r.stepFunc(m)
	}
	return nil
}

// Message handler for Follower
func (r *Raft) stepFollower(m pb.Message) error {
	switch m.MsgType {
	case pb.MessageType_MsgHup:
		r.becomeCandidate()
		for i := 1; i <= len(r.Prs); i++ {
			if uint64(i) == r.id {
				continue
			}
			r.sendRequestVote(uint64(i))
		}
	case pb.MessageType_MsgAppend:
		// TODO: Log replication
		fallthrough
	case pb.MessageType_MsgHeartbeat:
		r.becomeFollower(m.Term, m.From)
	}
	return nil
}

// Message handler for Candidate
func (r *Raft) stepCandidate(m pb.Message) error {
	switch m.MsgType {
	case pb.MessageType_MsgHup:
		r.becomeCandidate()
		for i := 1; i <= len(r.Prs); i++ {
			if uint64(i) == r.id {
				continue
			}
			r.sendRequestVote(uint64(i))
		}
	case pb.MessageType_MsgRequestVoteResponse:
		if m.Term != r.Term {
			log.Panicf("Node %d get RequestVote response with different term: want %d, get %d",
				r.id, r.Term, m.Term)
		}
		r.votes[m.From] = !m.Reject
		if !m.Reject {
			log.Infof("Node %d received vote from node %d at term %d", r.id, m.From, r.Term)
		} else {
			log.Infof("Node %d received rejection from node %d at term %d", r.id, m.From, r.Term)
		}
		res := r.tallyVotes()
		switch res {
		case VoteWon:
			r.becomeLeader()
			// broadcast first heartbeat
			for i := 1; i <= len(r.Prs); i++ {
				if uint64(i) == r.id {
					continue
				}
				r.sendHeartbeat(uint64(i))
			}
		case VoteLost:
			r.becomeFollower(r.Term, None)
		}
	case pb.MessageType_MsgAppend:
		// TODO: Log replication
		fallthrough
	case pb.MessageType_MsgHeartbeat:
		r.becomeFollower(m.Term, m.From)
	}
	return nil
}

// Message handler for Leader
func (r *Raft) stepLeader(m pb.Message) error {
	switch m.MsgType {
	case pb.MessageType_MsgBeat:
		for i := 1; i <= len(r.Prs); i++ {
			if uint64(i) == r.id {
				continue
			}
			r.sendHeartbeat(uint64(i))
		}
	case pb.MessageType_MsgPropose:
		// appends the proposal to its log as a new entry
		log.Infof("before append entry: unstable: %v new_entries_len: %d li: %d",
			r.RaftLog.entries, len(m.Entries), r.RaftLog.LastIndex())
		r.appendEntries(m.Entries...)
		log.Infof("after append entry: unstable: %v", r.RaftLog.entries)
		// broadcast Append message
		for i := 1; i <= len(r.Prs); i++ {
			if uint64(i) == r.id {
				continue
			}
			r.sendAppend(uint64(i))
		}
	// There is no need to handle MessageType_MsgHeartbeatResponse.
	// HeartbeatResponse only works when m.Term > r.Term，
	// but this situation has been dealt with in the Step method.
	case pb.MessageType_MsgAppendResponse:
		// TODO: Update Progress
		prs := r.Prs[m.From]
		if m.Reject {
			// ignore append responses that arrive late because of communication delay
			if m.Index == prs.Next-1 {
				// not a stale append response
				// decrease Next by 1 for now. (TODO) add index hint
				prs.Next = m.Index
				if prs.Next < 1 {
					prs.Next = 1
				}
				log.Infof("Leader Node %d receives a rejection append response, update Node %d Next -> %d",
					r.id, m.From, prs.Next)
				r.sendAppend(m.From)
			}
		} else {
			// ignore stale response
			// match, update Match
			if m.Index > prs.Match {
				prs.Match = m.Index
				prs.Next = m.Index + 1
				log.Infof("Leader Node %d receives a match append response, update Node %d (Match,Next) -> (%d,%d)",
					r.id, m.From, prs.Match, prs.Next)
				if r.MaybeUpdateCommit() {
					// broadcast Append message
					for i := 1; i <= len(r.Prs); i++ {
						if uint64(i) == r.id {
							continue
						}
						r.sendAppend(uint64(i))
					}
				}
			}
		}
	}
	return nil
}

func (r *Raft) MaybeUpdateCommit() bool {
	matches := make([]uint64, len(r.Prs))
	for i, prs := range r.Prs {
		if i == r.id {
			matches[i-1] = r.RaftLog.LastIndex()
		} else {
			matches[i-1] = prs.Match
		}
	}

	sortkeys.Uint64s(matches)
	mid := matches[(len(matches)-1)/2]
	log.Infof("[MaybeUpdayeCommit] matches: %v mid: %d", matches, mid)
	if mid > r.RaftLog.LastIndex() || mid <= r.RaftLog.committed {
		return false
	}
	midTerm, err := r.RaftLog.Term(mid)
	if err != nil {
		log.Errorf("[MaybeUpdateCommit] raftlog term call error")
		return false
	}
	if midTerm != r.Term {
		return false
	}
	log.Infof("Node %d update commit index from %d to %d", r.id, r.RaftLog.committed, mid)
	r.RaftLog.committed = mid
	return true
}

//
func (r *Raft) appendEntries(entries ...*pb.Entry) {
	lastIdx := r.RaftLog.LastIndex()
	ents := make([]pb.Entry, 0)
	for i := range entries {
		entries[i].Index = lastIdx + 1 + uint64(i)
		entries[i].Term = r.Term
		ents = append(ents, *entries[i])
	}
	r.RaftLog.append(ents...)
}

func (r *Raft) handleRequestVote(m pb.Message) {
	canVote := (r.Vote == None && r.Lead == None) || r.Vote == m.From
	isUptoDate := m.LogTerm > r.RaftLog.LastTerm() ||
		(m.LogTerm == r.RaftLog.LastTerm() && m.Index >= r.RaftLog.LastIndex())
	rejected := !(canVote && isUptoDate)
	if !rejected {
		r.Vote = m.From
	}
	r.sendRequestVoteResponse(m.From, rejected)
}

// handleAppendEntries handle AppendEntries RPC request
func (r *Raft) handleAppendEntries(m pb.Message) {
	// Your Code Here (2A).
}

// handleHeartbeat handle Heartbeat RPC request
func (r *Raft) handleHeartbeat(m pb.Message) {
	// Your Code Here (2A).
}

// handleSnapshot handle Snapshot RPC request
func (r *Raft) handleSnapshot(m pb.Message) {
	// Your Code Here (2C).
}

// addNode add a new node to raft group
func (r *Raft) addNode(id uint64) {
	// Your Code Here (3A).
}

// removeNode remove a node from raft group
func (r *Raft) removeNode(id uint64) {
	// Your Code Here (3A).
}

const (
	VoteWon = iota
	VotePending
	VoteLost
)

type VoteRes = int

func (r *Raft) tallyVotes() VoteRes {
	granted, rejected := 0, 0
	quota := len(r.Prs) / 2
	for i := 1; i <= len(r.Prs); i++ {
		v, voted := r.votes[uint64(i)]
		if !voted {
			continue
		}
		if v {
			granted++
		} else {
			rejected++
		}
	}
	if granted > quota {
		return VoteWon
	}
	if rejected <= quota {
		return VotePending
	}
	return VoteLost
}
