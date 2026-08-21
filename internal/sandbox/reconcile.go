package sandbox

import (
	"context"
	"fmt"
	"os/exec"
	"time"
)

// ReconcileResult says what startup reconciliation did with each ownership
// record: kept (container alive, TTL not passed), dropped (container vanished
// underneath us, record removed), or reaped (TTL passed, container destroyed
// and record removed). Errors keep their records — an unreachable engine must
// not erase ownership.
type ReconcileResult struct {
	Kept    []Record
	Dropped []Record
	Reaped  []Record
	Errors  []string
}

// Reconcile walks the ownership records and brings them back in line with
// reality: records for containers the engine no longer knows are dropped, and
// containers past their TTL are destroyed and their records removed.
func Reconcile(ctx context.Context, store *Store, now time.Time) ReconcileResult {
	var res ReconcileResult
	recs, err := store.List()
	if err != nil {
		res.Errors = append(res.Errors, err.Error())
		return res
	}
	for _, rec := range recs {
		enginePath, err := exec.LookPath(rec.Engine)
		if err != nil {
			res.Kept = append(res.Kept, rec)
			res.Errors = append(res.Errors, fmt.Sprintf("%s: engine %s not found, keeping record", rec.ID, rec.Engine))
			continue
		}
		_, gone, err := ContainerState(ctx, enginePath, rec.Name)
		switch {
		case err != nil:
			res.Kept = append(res.Kept, rec)
			res.Errors = append(res.Errors, fmt.Sprintf("%s: %v", rec.ID, err))
		case gone:
			if err := store.Remove(rec.ID); err != nil {
				res.Errors = append(res.Errors, fmt.Sprintf("%s: %v", rec.ID, err))
				continue
			}
			res.Dropped = append(res.Dropped, rec)
		case rec.Expired(now):
			if err := DestroyContainer(ctx, enginePath, store, rec); err != nil {
				res.Kept = append(res.Kept, rec)
				res.Errors = append(res.Errors, fmt.Sprintf("%s: %v", rec.ID, err))
				continue
			}
			res.Reaped = append(res.Reaped, rec)
		default:
			res.Kept = append(res.Kept, rec)
		}
	}
	return res
}
