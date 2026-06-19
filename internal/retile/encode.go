package retile

import "context"

// TileEncoder encodes one RGB tile (w×h, 3 bytes/px, stride w*3) to a
// self-contained compressed body. Implementations MUST be safe for concurrent
// EncodeTile calls (the engine shares one encoder across worker goroutines).
type TileEncoder interface {
	EncodeTile(rgb []byte, w, h int) ([]byte, error)
}

// TileSink receives encoded output tiles. level is the engine-relative level
// index (LevelSpec.Index); the sink translates it to its container numbering.
// The engine emits tiles for multiple levels INTERLEAVED and, within a level,
// out of grid order (the encoder pool finishes tiles out of order); a sink whose
// writer requires ordering must buffer/reorder internally.
type TileSink interface {
	WriteTile(level, col, row int, encoded []byte) error
}

// writeJob hands encoded bytes to the sink-drain goroutine.
type writeJob struct {
	level, col, row int
	body            []byte
}

// encoderWorker pulls encodeJobs, encodes via enc, and pushes writeJobs. The
// tile's RGB buffer is released back to the pool after encoding. Exits cleanly
// when jobs is closed or ctx is cancelled.
func encoderWorker(ctx context.Context, jobs <-chan encodeJob, out chan<- writeJob, enc TileEncoder) {
	for {
		select {
		case <-ctx.Done():
			return
		case job, ok := <-jobs:
			if !ok {
				return
			}
			body, err := enc.EncodeTile(job.img.Pix, job.img.W, job.img.H)
			releaseRGB(job.img)
			if err != nil {
				return
			}
			select {
			case out <- writeJob{level: job.level, col: job.col, row: job.row, body: body}:
			case <-ctx.Done():
				return
			}
		}
	}
}

// sinkDrainer pulls writeJobs and calls sink.WriteTile serially. Stores the
// first error in *firstErr; subsequent errors are dropped.
func sinkDrainer(jobs <-chan writeJob, sink TileSink, firstErr *error) {
	for job := range jobs {
		if err := sink.WriteTile(job.level, job.col, job.row, job.body); err != nil {
			if *firstErr == nil {
				*firstErr = err
			}
		}
	}
}
