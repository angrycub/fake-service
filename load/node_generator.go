package load

import (
	"math"
	"math/rand"
	"runtime"
	"time"

	"github.com/hashicorp/go-hclog"
)

// original code from:
// https://github.com/vikyd/go-cpu-load
// Thank you for your awesome work

// Finished should be called when a function exits to stop the load generation
// defined in generator.go

type NodeGenerator struct {
	logger               hclog.Logger
	cpuCoresCount        float64
	cpuPercentage        float64
	memoryMBytes         int
	memoryVariance       int
	memoryVarianceFun    string
	memoryVariancePeriod int
	running              bool
	currentMemory        int
	currentTick          int
	finished             chan struct{}
}

const TICK_INTERVAL = 500 * time.Millisecond

// NewGenerator creates a new load generator that can create artificial memory and cpu pressure
func NewNodeGenerator(cores, percentage float64, memoryMBytes, memoryVariance int, memoryVarianceFun string, memoryVariancePeriod int, logger hclog.Logger) *NodeGenerator {
	return &NodeGenerator{
		logger,
		cores,
		percentage,
		memoryMBytes,
		memoryVariance,
		memoryVarianceFun,
		memoryVariancePeriod,
		false,
		memoryMBytes * int(math.Pow(2, 20)),
		0,
		nil,
	}
}

// Generate load for the request
func (g *NodeGenerator) Generate() Finished {
	// this needs to be a buffered channel or the return function will block and leak
	g.finished = make(chan struct{}, 2)
	g.running = true

	// generate the memory first to ensure that the CPU consumption
	// does not block memory creation
	g.generateVaryingMemory()
	g.generateCPU()

	return func() {
		// call finished twice for memory and CPU
		g.finished <- struct{}{}
		g.finished <- struct{}{}
		g.running = false
	}
}

// RunCPULoad run CPU load in specify cores count and percentage
func (g *NodeGenerator) generateCPU() {
	if g.cpuCoresCount == 0 {
		return
	}

	go func() {
		g.logger.Info("Generating CPU Load", "cores", g.cpuCoresCount, "percentage", g.cpuPercentage)

		runtime.GOMAXPROCS(int(g.cpuCoresCount))

		// second     ,s  * 1
		// millisecond,ms * 1000
		// microsecond,μs * 1000 * 1000
		// nanosecond ,ns * 1000 * 1000 * 1000

		// every loop : run + sleep = 1 unit

		// 1 unit = 100 ms may be the best
		var unitHundredsOfMicrosecond float64 = 1000
		runMicrosecond := unitHundredsOfMicrosecond * g.cpuPercentage
		sleepMicrosecond := unitHundredsOfMicrosecond*100 - runMicrosecond
		for i := 0; i < int(g.cpuCoresCount); i++ {
			go func() {
				runtime.LockOSThread()
				// endless loop
				for g.running {
					begin := time.Now()
					for {
						// run 100%
						if time.Since(begin) > time.Duration(runMicrosecond)*time.Microsecond {
							break
						}
					}
					// sleep
					time.Sleep(time.Duration(sleepMicrosecond) * time.Microsecond)
				}
			}()
		}

		// block until signal to complete load generation is received
		<-g.finished
	}()
}

func (g *NodeGenerator) generateVaryingMemory() {
	calculateVariance := getVarianceFuncByName(g.memoryVarianceFun)

	go func() {
		for g.running {
			start := time.Now()
			newMemLen := calculateVariance(g)

			mem := make([]byte, 0, newMemLen)
			_ = mem

			// print the memory consumption
			var m runtime.MemStats
			runtime.ReadMemStats(&m)
			g.currentMemory = newMemLen
			g.logger.Debug("Allocated memory", "MB", bToMb(m.Alloc), "mem", newMemLen)
			time.Sleep(TICK_INTERVAL - time.Since(start))
		}
		// block until signal to complete load generation is received
		<-g.finished
	}()
}

type varianceFunc func(*NodeGenerator) int

func varianceLinear(g *NodeGenerator) int {
	return g.currentMemory
}

func varianceRandom(g *NodeGenerator) int {
	if g.memoryVariance > 0 {
		// convert mib to bytes
		fMemoryMBytes := float64(g.memoryMBytes)
		fMemoryBytes := fMemoryMBytes * math.Pow(2, 20)
		rPct := rand.Float64()*2 - 1
		varyByPct := float64(g.memoryVariance) * rPct / 100
		delta := fMemoryBytes * varyByPct

		newValue := int(fMemoryBytes + delta)

		g.logger.Debug(
			"varianceRandom",
			"rPct", rPct,
			"varyByPct", varyByPct,
			"delta", delta,
			"newValue", newValue,
		)

		return newValue
	} else {
		return g.currentMemory
	}
}

func varianceSineWave(g *NodeGenerator) int {
	if g.memoryVariance > 0 {
		varianceDuration := time.Duration(g.memoryVariancePeriod) * time.Second
		ticksPerPeriod := int(varianceDuration / TICK_INTERVAL)

		angle := float64(g.currentTick) * math.Pi
		sin := math.Sin(float64(g.currentTick/g.memoryVariancePeriod) * math.Pi)
		delta := sin * float64(g.memoryVariance)

		if g.currentTick+1 < ticksPerPeriod {
			g.currentTick = g.currentTick + 1
		} else {
			g.currentTick = 0
		}

		g.logger.Info(
			"varianceSineWave",
			"ticksPerPeriod", ticksPerPeriod,
			"currentTick", g.currentTick,
			"angle", angle,
			"delta", delta,
			"current_memory", g.currentMemory,
		)
		return g.currentMemory

	}
	return g.currentMemory
}

func getVarianceFuncByName(varianceFunName string) varianceFunc {
	switch varianceFunName {
	case "linear":
		return varianceLinear
	case "random":
		return varianceRandom
	case "sine":
		return varianceSineWave
	default:
		return varianceLinear
	}
}
