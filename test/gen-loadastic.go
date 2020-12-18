// This file was automatically generated by genny.
// Any changes will be lost if this file is regenerated.
// see https://github.com/cheekybits/genny

package test

import (
	"runtime"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/slinkydeveloper/loadastic/common"
	vegeta "github.com/tsenart/vegeta/v12/lib"
)

type BeforeSend func(request mockRequest, tickerTimestamp time.Time, id uint64, uuid string)
type AfterSend func(request mockRequest, response mockResponse, id uint64, uuid string)
type AfterFailed func(request mockRequest, err error, id uint64, uuid string)

type RequestFactory func(tickerTimestamp time.Time, id uint64, uuid string) mockRequest
type FailedChecker func(response mockResponse) error

type Sender interface {
	InitializeWorker() interface{}
	Send(worker interface{}, request mockRequest) (mockResponse, error)
}

type Loadastic struct {
	failedChecker  FailedChecker
	sender         Sender
	initialWorkers uint

	beforeSend  BeforeSend
	afterSend   AfterSend
	afterFailed AfterFailed
}

func NewLoadastic(sender Sender, opts ...func(*Loadastic)) Loadastic {
	l := Loadastic{
		sender:         sender,
		initialWorkers: 10,
	}

	for _, f := range opts {
		f(&l)
	}

	return l
}

func WithFailedChecker(checker FailedChecker) func(*Loadastic) {
	return func(loadastic *Loadastic) {
		loadastic.failedChecker = checker
	}
}

func WithBeforeSend(beforeSend BeforeSend) func(*Loadastic) {
	return func(loadastic *Loadastic) {
		loadastic.beforeSend = beforeSend
	}
}

func WithAfterSend(afterSend AfterSend) func(*Loadastic) {
	return func(loadastic *Loadastic) {
		loadastic.afterSend = afterSend
	}
}

func WithAfterFailed(afterFailed AfterFailed) func(*Loadastic) {
	return func(loadastic *Loadastic) {
		loadastic.afterFailed = afterFailed
	}
}

func WithInitialWorkers(initialWorkers uint) func(*Loadastic) {
	return func(loadastic *Loadastic) {
		loadastic.initialWorkers = initialWorkers
	}
}

func (l *Loadastic) StartSteps(requestFactory RequestFactory, steps ...common.Step) {
	for _, s := range steps {
		l.ExecutePace(requestFactory, vegeta.ConstantPacer{Freq: int(s.Rps), Per: time.Second}, s.Duration)
	}
}

func (l *Loadastic) ExecutePace(requestFactory RequestFactory, pacer vegeta.Pacer, duration time.Duration) {
	workers := sync.WaitGroup{}
	jobsPool := sync.Pool{
		New: func() interface{} {
			return &common.Job{}
		},
	}
	jobsCh := make(chan *common.Job)

	for i := uint(0); i < l.initialWorkers; i++ {
		go l.worker(requestFactory, &workers, jobsCh, &jobsPool)
	}
	workers.Add(int(l.initialWorkers))

	began, count := time.Now(), uint64(0)
	for {
		elapsed := time.Since(began)
		if duration > 0 && elapsed > duration {
			break
		}

		wait, stop := pacer.Pace(elapsed, count)
		if stop {
			break
		}

		time.Sleep(wait)

		// Create the job
		job := jobsPool.Get().(*common.Job)
		job.Id = count
		job.Timestamp = time.Now()

		// Try to run into actual worker pool
		select {
		case jobsCh <- job: // Not blocking try to put in channel
			count++
			continue
		default:
			workers.Add(1)
			go l.worker(requestFactory, &workers, jobsCh, &jobsPool)
		}
	}

	close(jobsCh)
	workers.Wait()

	runtime.GC()
}

func (l *Loadastic) worker(requestFactory RequestFactory, workersCount *sync.WaitGroup, jobs <-chan *common.Job, jobsPool *sync.Pool) {
	defer workersCount.Done()
	workerResource := l.sender.InitializeWorker()
	for j := range jobs {
		// Generate UUID (required for distributed tests)
		uuid := uuid.New().String()

		// Create the request
		req := requestFactory(j.Timestamp, j.Id, uuid)

		if l.beforeSend != nil {
			l.beforeSend(req, j.Timestamp, j.Id, uuid)
		}

		// Send the request
		res, err := l.sender.Send(workerResource, req)

		if err != nil {
			if l.afterFailed != nil {
				l.afterFailed(req, err, j.Id, uuid)
			}
			continue
		}

		// Check if failed
		if l.failedChecker != nil {
			err = l.failedChecker(res)
			if err != nil {
				if l.afterFailed != nil {
					l.afterFailed(req, err, j.Id, uuid)
				}
				continue
			}
		}

		if l.afterSend != nil {
			l.afterSend(req, res, j.Id, uuid)
		}

		jobsPool.Put(j)
	}
}
