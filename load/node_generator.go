package load

import (
	"fmt"
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

type varianceFunc func(*NodeGenerator) int

type NodeGenerator struct {
	logger               hclog.Logger
	cpuCoresCount        float64
	cpuPercentage        float64
	memoryMBytes         int
	memoryVariance       int // variance in percent
	memoryVarianceFun    string
	memoryVariancePeriod int
	running              bool
	state                *NodeGeneratorState
	finished             chan struct{}
}

type NodeGeneratorState struct {
	baselineBytes    int       // memoryMBytes as bytes
	maxVarianceBytes float64   // variancePercent of baselineBytes as a float
	currentBytes     int       // memory in bytes - 1 MiB = 2^20 bytes
	currentTick      int       // value in the interval [0,ticksPerPeriod)
	startTime        time.Time // time the NodeGenerator was started
	lastTickTime     time.Time // time the last tick started
	ticksPerPeriod   int       // number of ticks that fit in the given period based on TICK_DURATION
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
		&NodeGeneratorState{
			memoryMBytes * int(math.Pow(2, 20)),
			math.Pow(2, 20) * float64(memoryMBytes*memoryVariance) / 100,
			memoryMBytes * int(math.Pow(2, 20)),
			0,
			time.Now(),
			time.Now(),
			int(time.Duration(memoryVariancePeriod) * time.Second / TICK_INTERVAL),
		},
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
	delta := g.getVarianceFuncByName()

	go func() {
		g.state.startTime = time.Now()
		for g.running {
			g.state.lastTickTime = time.Now()

			newMemLen := g.state.currentBytes + delta(g)

			mem := make([]byte, 0, newMemLen)
			_ = mem

			// print the memory consumption
			var m runtime.MemStats
			runtime.ReadMemStats(&m)
			g.state.currentBytes = newMemLen
			g.logger.Debug("Allocated memory", "MB", bToMb(m.Alloc), "mem", newMemLen)
			g.tick()
			time.Sleep(TICK_INTERVAL - time.Since(g.state.lastTickTime)) // it's fast, but not free.
		}
		// block until signal to complete load generation is received
		<-g.finished
	}()
}

func varianceLinear(g *NodeGenerator) int {
	lVal := 2*g.linearX()*g.state.maxVarianceBytes - g.state.maxVarianceBytes
	delta := int(lVal)

	g.logger.Info(
		"varianceLinear",
		"Tick", g.xAsFrac(),
		"lPct", fmt.Sprintf("%0.5f", lVal),
		"delta", bytesToMiBString(delta),
	)
	return delta
}

func varianceRandom(g *NodeGenerator) int {
	rSign := rand.Intn(2) * -1                            // flip a coin for sign [0,1] -> [-1,0]
	rPct := math.Copysign(rand.Float64(), float64(rSign)) // roll a random [0-1) and apply sign
	delta := int(rPct * g.state.maxVarianceBytes)

	g.logger.Debug(
		"varianceRandom",
		"Tick", g.xAsFrac(),
		"rPct", rPct,
		"delta", bytesToMiBString(delta),
	)

	return delta
}

func varianceSineWave(g *NodeGenerator) int {
	rads := g.rad()
	sin := math.Sin(rads)
	delta := int(sin * g.state.maxVarianceBytes)

	g.logger.Debug(
		"varianceSineWave",
		"Tick", g.xAsFrac(),
		"x", fmt.Sprintf("%0.5f", g.x()),
		"angle", int(g.deg()),
		"rads", g.radString(false),
		"sin", fmt.Sprintf("%0.5f", sin),
		"delta", bytesToMiBString(delta),
	)

	return delta
}

func (g *NodeGenerator) getVarianceFuncByName() varianceFunc {
	varianceZero := func(_ *NodeGenerator) int { return 0 }
	if g.memoryVariance == 0 {
		return varianceZero
	}
	switch g.memoryVarianceFun {
	case "linear":
		return varianceLinear
	case "random":
		return varianceRandom
	case "sine":
		return varianceSineWave
	default:
		return varianceZero
	}
}

func bytesToMiBString(bytes int) string {
	return fmt.Sprintf("%0.2f MiB", float64(bytes)*math.Pow(2, -20))
}

// deg converts x in degrees
func (g *NodeGenerator) deg() float64 {
	return 360 * g.x()
}

// rad converts x to radians
func (g *NodeGenerator) rad() float64 {
	return 2 * math.Pi * g.x()
}

// radString makes a pretty fractional view of the radius for output
func (g *NodeGenerator) radString(shouldReduce bool) string {
	num, denom := g.state.currentTick*2, g.state.ticksPerPeriod
	if shouldReduce {
		num, denom = reduce(g.state.currentTick*2, g.state.ticksPerPeriod)
	}
	if num == 0 {
		return "0"
	}
	// 2π is not in the period since it's adjusted to [0,2π)
	if denom == 1 {
		return "π"
	}
	if num == 1 {
		return fmt.Sprintf("π/%d", denom)
	}
	return fmt.Sprintf("%dπ/%d", num, denom)
}

func (g *NodeGenerator) xAsFrac() string {
	return fmt.Sprintf("%d/%d", g.state.currentTick, g.state.ticksPerPeriod)
}

// x returns the fraction created from currentTick over Ticks per period,
// effectively mapping to an interval of [0,1)
func (g *NodeGenerator) x() float64 {
	x0 := g.state.currentTick
	x1 := g.state.ticksPerPeriod
	val := float64(x0) / float64(x1)
	return val
}

// x returns the fraction created from currentTick over Ticks per period - 1,
// effectively mapping to an interval of [0,1]
func (g *NodeGenerator) linearX() float64 {
	x0 := g.state.currentTick
	x1 := g.state.ticksPerPeriod
	val := float64(x0) / float64(x1-1)
	return val
}

func (g *NodeGenerator) linearXAsFrac() string {
	return fmt.Sprintf("%d/%d", g.state.currentTick, g.state.ticksPerPeriod-1)
}

// tick just moves time forward by one in the state
func (g *NodeGenerator) tick() {
	g.state.currentTick = (g.state.currentTick + 1) % g.state.ticksPerPeriod
}

// reduce fraction parts for prettiness
func reduce(num, denom int) (int, int) {
	if num == 0 {
		return num, denom
	}
	gcd := gcd(num, denom)
	if gcd == 1 {
		return num, denom
	}
	return num / gcd, denom / gcd
}

// gcd returns the greatest common divisor (GCD) via Euclidean algorithm
func gcd(a, b int) int {
	for b != 0 {
		t := b
		b = a % b
		a = t
	}
	return a
}
