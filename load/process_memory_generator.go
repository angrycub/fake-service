package load

import (
	"runtime"
	"time"

	"github.com/hashicorp/go-hclog"
)

type ProcessMemoryGenerator struct {
	logger   hclog.Logger
	running  bool
	finished chan struct{}
}

type ProcessMemoryGeneratorConfig struct {
	BaselineMemory int // Baseline memory to allocate in MiB
	VariableMemory *VariableMemoryConfig
}

type VariableMemoryConfig struct {
	Period    int
	Generator string
}

// Starts the Generator
func (pmg *ProcessMemoryGenerator) Generate() Finished {
	// this needs to be a buffered channel or the return function will block and
	// leak
	pmg.finished = make(chan struct{}, 2)
	pmg.running = true

	pmg.generateVaryingMemory()

	return func() {
		g.finished <- struct{}{}
		g.running = false
	}
}

func (pmg *ProcessMemoryGenerator) generateVaryingMemory() {
	go func() {
		g.state.startTime = time.Now()
		for g.running {
			g.state.lastTickTime = time.Now()
			newMemLen := calculateNewMemory(g)
			mem := make([]byte, 0, newMemLen)
			_ = mem
			// print the memory consumption
			var m runtime.MemStats
			runtime.ReadMemStats(&m)
			g.state.currentBytes = newMemLen
			g.logger.Debug("Allocated memory", "MB", bToMb(m.Alloc), "mem", newMemLen)
			g.tick()
		}
		// block until signal to complete load generation is received
		<-g.finished
	}()
}

type Range struct {
	start int
	end   int
}

type RangeMap struct {
	input  Range
	output Range
}

func newRangeMap(input, output Range) *RangeMap {
	return &RangeMap(
		input,
		output,
	)
}
