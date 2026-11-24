package service

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

// StreamAccountingLogExport closes the log cursor before querying accounting.
// This also works when main/log stores share a single-connection SQLite pool:
// querying funds while its log cursor is open would wait on that same connection.
// A private temporary spool bounds memory, preserves the original stream order,
// and batches source lookups; it never becomes another financial record store.
func StreamAccountingLogExport(ctx context.Context, all bool, userID, role int, filter model.LogFilter, write func(*model.Log) error) error {
	spool, err := os.CreateTemp("", "silvermoon-log-export-*")
	if err != nil {
		return err
	}
	defer os.Remove(spool.Name())
	defer spool.Close()
	buffer := bufio.NewWriter(spool)
	retain := func(log *model.Log) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		encoded, err := common.Marshal(log)
		if err != nil {
			return err
		}
		_, err = buffer.Write(append(encoded, '\n'))
		return err
	}
	if all {
		err = model.StreamAllLogs(ctx, filter, role, retain)
	} else {
		err = model.StreamUserLogs(ctx, userID, filter, retain)
	}
	if err != nil {
		return err
	}
	if err = buffer.Flush(); err != nil {
		return err
	}
	if _, err = spool.Seek(0, io.SeekStart); err != nil {
		return err
	}
	reader := bufio.NewReader(spool)
	for {
		if err = ctx.Err(); err != nil {
			return err
		}
		batch := make([]*model.Log, 0, 100)
		for range 100 {
			line, readErr := reader.ReadBytes('\n')
			if readErr == io.EOF {
				break
			}
			if readErr != nil {
				return readErr
			}
			var log model.Log
			if err = common.Unmarshal(line, &log); err != nil {
				return err
			}
			batch = append(batch, &log)
		}
		if len(batch) == 0 {
			return nil
		}
		if err = EnrichMarketingUsageLogs(ctx, batch); err != nil {
			return err
		}
		for _, log := range batch {
			if err = write(log); err != nil {
				return err
			}
		}
	}
}

const logExportLockTTL = time.Minute

const logExportLockRenewScript = `
if redis.call('GET', KEYS[1]) == ARGV[1] then
  return redis.call('PEXPIRE', KEYS[1], ARGV[2])
end
return 0`

const logExportLockReleaseScript = `
if redis.call('GET', KEYS[1]) == ARGV[1] then
  return redis.call('DEL', KEYS[1])
end
return 0`

// logExportLocks holds the running exports when Redis is disabled.
var logExportLocks sync.Map

// AcquireLogExportLock reserves the one log export a user may run at a time.
// With Redis the reservation spans all nodes and lapses within a minute if
// this node dies. ok is false while another export of the user is running;
// otherwise release must be called when the export ends.
func AcquireLogExportLock(userId int) (release func(), ok bool, err error) {
	if !common.RedisEnabled {
		if _, running := logExportLocks.LoadOrStore(userId, struct{}{}); running {
			return nil, false, nil
		}
		return func() { logExportLocks.Delete(userId) }, true, nil
	}

	ctx := context.Background()
	key := fmt.Sprintf("log_export:%d", userId)
	owner := common.GetRandomString(16)
	acquired, err := common.RDB.SetNX(ctx, key, owner, logExportLockTTL).Result()
	if err != nil || !acquired {
		return nil, false, err
	}

	stop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(logExportLockTTL / 3)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				common.RDB.Eval(ctx, logExportLockRenewScript, []string{key}, owner, logExportLockTTL.Milliseconds())
			}
		}
	}()
	return func() {
		close(stop)
		common.RDB.Eval(ctx, logExportLockReleaseScript, []string{key}, owner)
	}, true, nil
}
