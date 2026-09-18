package analysis

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
	"github.com/DoplexLabs/belay-engine/internal/storage/local"
)

type RecurrenceReport struct {
	Claimed      int `json:"claimed"`
	Batches      int `json:"batches"`
	Processed    int `json:"processed"`
	Observations int `json:"observations"`
	Completed    int `json:"completed"`
	Failed       int `json:"failed"`
	Stale        int `json:"stale"`
}

type RecurrenceWorker struct {
	store        *local.Store
	reconciler   *Reconciler
	now          func() time.Time
	lock         *sync.Mutex
	processBatch func(
		context.Context,
		model.FixRecurrenceJobClaim,
		int,
	) (model.FixRecurrenceBatchResult, error)
}

var recurrenceWorkerLocks sync.Map

var ErrFixMonitoringCatchupPending = errors.New(
	"fix monitoring catch-up remains pending",
)

func NewRecurrenceWorker(store *local.Store) *RecurrenceWorker {
	lockValue, _ := recurrenceWorkerLocks.LoadOrStore(store, &sync.Mutex{})
	return &RecurrenceWorker{
		store:        store,
		reconciler:   NewReconciler(store),
		now:          func() time.Time { return time.Now().UTC() },
		lock:         lockValue.(*sync.Mutex),
		processBatch: store.ProcessRecurrenceBatch,
	}
}

func (w *RecurrenceWorker) Startup(
	ctx context.Context,
) (RecurrenceReport, error) {
	if w == nil || w.store == nil {
		return RecurrenceReport{}, errors.New("recurrence worker requires a store")
	}
	if err := ctx.Err(); err != nil {
		return RecurrenceReport{}, err
	}
	w.lock.Lock()
	defer w.lock.Unlock()
	if err := w.store.PrepareFixMonitoringCatchup(ctx); err != nil {
		w.recordCatchupFailure(ctx, "catchup_prepare_failed")
		return RecurrenceReport{}, err
	}
	if _, err := w.reconciler.Drain(ctx); err != nil {
		if !errors.Is(err, context.Canceled) {
			w.recordCatchupFailure(ctx, "catchup_reanalysis_failed")
		}
		return RecurrenceReport{}, err
	}
	report, err := w.drainLocked(ctx)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			w.recordCatchupFailure(ctx, "catchup_worker_failed")
		}
		return report, err
	}
	completed, err := w.store.CompleteFixMonitoringCatchup(ctx)
	if err != nil {
		w.recordCatchupFailure(ctx, "catchup_completion_failed")
		return report, err
	}
	if !completed {
		return report, ErrFixMonitoringCatchupPending
	}
	return report, nil
}

func (w *RecurrenceWorker) Drain(
	ctx context.Context,
) (RecurrenceReport, error) {
	if w == nil || w.store == nil {
		return RecurrenceReport{}, errors.New("recurrence worker requires a store")
	}
	w.lock.Lock()
	defer w.lock.Unlock()
	report, err := w.drainLocked(ctx)
	if err != nil {
		return report, err
	}
	if err := w.completeCatchupIfNeeded(ctx); err != nil {
		return report, err
	}
	return report, nil
}

// Recover is the steady-state Local runtime cycle. Analysis retries must run
// before recurrence jobs and readiness completion so a durable failed session
// can become current and enqueue the job that lets catch-up converge.
func (w *RecurrenceWorker) Recover(
	ctx context.Context,
) (RecurrenceReport, error) {
	if w == nil || w.store == nil || w.reconciler == nil {
		return RecurrenceReport{}, errors.New("recurrence worker requires a store")
	}
	if err := ctx.Err(); err != nil {
		return RecurrenceReport{}, err
	}
	w.lock.Lock()
	defer w.lock.Unlock()
	if _, err := w.reconciler.Drain(ctx); err != nil {
		return RecurrenceReport{}, err
	}
	report, err := w.drainLocked(ctx)
	if err != nil {
		return report, err
	}
	if err := w.completeCatchupIfNeeded(ctx); err != nil {
		return report, err
	}
	return report, nil
}

func (w *RecurrenceWorker) completeCatchupIfNeeded(ctx context.Context) error {
	readiness, err := w.store.FixMonitoringReadiness(ctx)
	if err != nil {
		return err
	}
	if readiness != model.FixMonitoringReadinessCatchingUp {
		return nil
	}
	_, err = w.store.CompleteFixMonitoringCatchup(ctx)
	return err
}

func (w *RecurrenceWorker) drainLocked(
	ctx context.Context,
) (RecurrenceReport, error) {
	var report RecurrenceReport
	for {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		claim, err := w.store.ClaimRecurrenceJob(
			ctx,
			w.now(),
			defaultWorkerLease,
		)
		if errors.Is(err, local.ErrNoRecurrenceJob) {
			return report, nil
		}
		if err != nil {
			return report, err
		}
		report.Claimed++
		for {
			batch, err := w.processBatch(
				ctx,
				claim,
				100,
			)
			if errors.Is(err, local.ErrStaleRecurrenceJob) {
				report.Stale++
				break
			}
			if err != nil {
				retryAt := w.now().Add(recurrenceRetryDelay(claim.AttemptCount))
				if failErr := w.store.FailRecurrenceJob(
					ctx,
					claim,
					"recurrence_evaluation_failed",
					retryAt,
				); failErr != nil &&
					!errors.Is(failErr, local.ErrStaleRecurrenceJob) {
					return report, failErr
				}
				report.Failed++
				break
			}
			report.Batches++
			report.Processed += batch.Processed
			report.Observations += batch.Observed
			claim.AttemptAfterSequence = batch.NextSequence
			if batch.Completed {
				report.Completed++
				break
			}
		}
	}
}

const defaultWorkerLease = 30 * time.Second

func recurrenceRetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 7 {
		attempt = 7
	}
	return time.Duration(1<<(attempt-1)) * time.Second
}

func (w *RecurrenceWorker) recordCatchupFailure(
	ctx context.Context,
	code string,
) {
	if errors.Is(ctx.Err(), context.Canceled) {
		return
	}
	_ = w.store.FailFixMonitoringCatchup(
		context.Background(),
		code,
		w.now().Add(recurrenceRetryDelay(1)),
	)
}
