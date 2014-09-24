package ledis

import (
	"bytes"
	"errors"
	"github.com/siddontang/go/log"
	"github.com/siddontang/ledisdb/rpl"
	"io"
	"time"
)

const (
	maxReplLogSize = 1 * 1024 * 1024
)

var (
	ErrLogMissed = errors.New("log is pured in server")
)

func (l *Ledis) ReplicationUsed() bool {
	return l.r != nil
}

func (l *Ledis) handleReplication() {
	l.commitLock.Lock()
	defer l.commitLock.Unlock()

	l.rwg.Add(1)
	rl := &rpl.Log{}
	for {
		if err := l.r.NextNeedCommitLog(rl); err != nil {
			if err != rpl.ErrNoBehindLog {
				log.Error("get next commit log err, %s", err.Error)
			} else {
				l.rwg.Done()
				return
			}
		} else {
			l.rbatch.Rollback()
			decodeEventBatch(l.rbatch, rl.Data)

			if err := l.rbatch.Commit(); err != nil {
				log.Error("commit log error %s", err.Error())
			} else if err = l.r.UpdateCommitID(rl.ID); err != nil {
				log.Error("update commit id error %s", err.Error())
			}
		}

	}
}

func (l *Ledis) onReplication() {
	AsyncNotify(l.rc)

	for {
		select {
		case <-l.rc:
			l.handleReplication()
		case <-time.After(5 * time.Second):
			l.handleReplication()
		}
	}
}

func (l *Ledis) WaitReplication() error {
	if !l.ReplicationUsed() {
		return ErrRplNotSupport

	}
	AsyncNotify(l.rc)

	l.rwg.Wait()

	b, err := l.r.CommitIDBehind()
	if err != nil {
		return err
	} else if b {
		AsyncNotify(l.rc)
		l.rwg.Wait()
	}

	return nil
}

func (l *Ledis) StoreLogsFromReader(rb io.Reader) error {
	if !l.ReplicationUsed() {
		return ErrRplNotSupport
	} else if !l.readOnly {
		return ErrRplInRDWR
	}

	log := &rpl.Log{}

	for {
		if err := log.Decode(rb); err != nil {
			if err == io.EOF {
				break
			} else {
				return err
			}
		}

		if err := l.r.StoreLog(log); err != nil {
			return err
		}

	}

	AsyncNotify(l.rc)

	return nil
}

func (l *Ledis) StoreLogsFromData(data []byte) error {
	rb := bytes.NewReader(data)

	return l.StoreLogsFromReader(rb)
}

func (l *Ledis) ReadLogsTo(startLogID uint64, w io.Writer) (n int, nextLogID uint64, err error) {
	if !l.ReplicationUsed() {
		// no replication log
		nextLogID = 0
		err = ErrRplNotSupport
		return
	}

	var firtID, lastID uint64

	firtID, err = l.r.FirstLogID()
	if err != nil {
		return
	}

	if startLogID < firtID {
		err = ErrLogMissed
		return
	}

	lastID, err = l.r.LastLogID()
	if err != nil {
		return
	}

	nextLogID = startLogID

	log := &rpl.Log{}
	for i := startLogID; i <= lastID; i++ {
		if err = l.r.GetLog(i, log); err != nil {
			return
		}

		if err = log.Encode(w); err != nil {
			return
		}

		nextLogID = i + 1

		n += log.Size()

		if n > maxReplLogSize {
			break
		}
	}

	return
}

// try to read events, if no events read, try to wait the new event singal until timeout seconds
func (l *Ledis) ReadLogsToTimeout(startLogID uint64, w io.Writer, timeout int) (n int, nextLogID uint64, err error) {
	n, nextLogID, err = l.ReadLogsTo(startLogID, w)
	if err != nil {
		return
	} else if n != 0 {
		return
	}
	//no events read
	select {
	case <-l.r.WaitLog():
	case <-time.After(time.Duration(timeout) * time.Second):
	}
	return l.ReadLogsTo(startLogID, w)
}

func (l *Ledis) NextSyncLogID() (uint64, error) {
	if !l.ReplicationUsed() {
		return 0, ErrRplNotSupport
	}

	s, err := l.r.Stat()
	if err != nil {
		return 0, err
	}

	if s.LastID > s.CommitID {
		return s.LastID + 1, nil
	} else {
		return s.CommitID + 1, nil
	}
}

func (l *Ledis) propagate(rl *rpl.Log) {
	for _, h := range l.rhs {
		h(rl)
	}
}

type NewLogEventHandler func(rl *rpl.Log)

func (l *Ledis) AddNewLogEventHandler(h NewLogEventHandler) error {
	if !l.ReplicationUsed() {
		return ErrRplNotSupport
	}

	l.rhs = append(l.rhs, h)

	return nil
}

func (l *Ledis) ReplicationStat() (*rpl.Stat, error) {
	if !l.ReplicationUsed() {
		return nil, ErrRplNotSupport
	}

	return l.r.Stat()
}
