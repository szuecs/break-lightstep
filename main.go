package main

import (
	"flag"
	"io"
	"math/rand"
	"strconv"
	"strings"
	"time"

	"github.com/lightstep/lightstep-tracer-go"
	"github.com/opentracing/opentracing-go"
	log "github.com/sirupsen/logrus"
)

var (
	randomChars = []byte("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789")
)

type random struct {
	spanName string
	rand     *rand.Rand
	tracer   lightstep.Tracer
}

func (r *random) Read(p []byte) (int, error) {
	for i := 0; i < len(p); i++ {
		p[i] = randomChars[r.rand.Intn(len(randomChars))]
	}
	return len(p), nil
}
func (r *random) createSpans(num int, size int64) {
	log.Infof("create %d spans of size %d", num, size)
	for i := 0; i < num; i++ {
		r.createSpan(size)
	}
}
func (r *random) createSpan(size int64) {
	name, err := io.ReadAll(io.LimitReader(r, 10))
	if err != nil {
		log.Fatalf("Failed to read name: %v", err)
	}

	value, err := io.ReadAll(io.LimitReader(r, size-10))
	if err != nil {
		log.Fatalf("Failed to read value: %v", err)
	}

	span := r.tracer.StartSpan(r.spanName)
	defer span.Finish()
	span.SetTag(string(name), string(value))
}

func main() {
	r := &random{
		rand: rand.New(rand.NewSource(time.Now().UnixNano())),
	}

	var (
		useGRPC            bool
		logEvents          bool
		spanName           string
		componentName      string
		token              string
		hostPort           string
		maxBufferedSpans   int
		grpcMaxMsgSize     int
		genSpans           int
		genSpanSize        int64
		genInterval        time.Duration
		maxReportingPeriod time.Duration
		minReportingPeriod time.Duration
		stopAfter          time.Duration

		propagators = make(map[opentracing.BuiltinFormat]lightstep.Propagator)
	)
	flag.BoolVar(&useGRPC, "grpc", true, "enable to use GRPC")
	flag.BoolVar(&logEvents, "log-events", true, "enable to log all events")
	flag.StringVar(&spanName, "span-name", "myspan", "set span name")
	flag.StringVar(&componentName, "component-name", "mycomponent", "set component name")
	flag.StringVar(&token, "token", "", "set lighstep token")
	flag.StringVar(&hostPort, "host", "", "set destination to satellites")
	flag.IntVar(&maxBufferedSpans, "max-buffered-spans", 1024, "set maxbufferedspans")
	flag.IntVar(&grpcMaxMsgSize, "max-grpc-msg-size", 102400, "set max grpc message size")
	flag.DurationVar(&maxReportingPeriod, "max-reporting-period", 2500*time.Millisecond, "set max reporting period")
	flag.DurationVar(&minReportingPeriod, "min-reporting-period", 500*time.Millisecond, "set min reporting period")

	flag.IntVar(&genSpans, "generate-spans", 10, "number of spans to generate")
	flag.Int64Var(&genSpanSize, "generate-span-size", 1024, "size of one span")
	flag.DurationVar(&genInterval, "generate-interval", 50*time.Millisecond, "generate span interval")
	flag.DurationVar(&stopAfter, "stop-after", time.Second, "")

	flag.Parse()

	r.spanName = spanName
	if genSpanSize <= 10 {
		log.Fatalf("Span size has to be bigger than 10, %d", genSpanSize)
	}

	a := strings.Split(hostPort, ":")
	if len(a) != 2 {
		log.Fatalf("-host=<hostport> hostport needs to be host:port, but is %s", hostPort)
	}
	host, portS := a[0], a[1]
	port, err := strconv.Atoi(portS)
	if err != nil {
		log.Fatalf("Failed to convert port to number: %v", err)
	}

	prStack := lightstep.PropagatorStack{}
	prStack.PushPropagator(lightstep.LightStepPropagator)
	propagators[opentracing.HTTPHeaders] = prStack

	if logEvents {
		lightstep.SetGlobalEventHandler(createEventLogger())
	}

	tags := map[string]interface{}{
		lightstep.ComponentNameKey: componentName,
	}

	opts := lightstep.Options{
		AccessToken: token,
		Collector: lightstep.Endpoint{
			Host:      host,
			Port:      port,
			Plaintext: false,
		},
		UseGRPC:                     useGRPC,
		Tags:                        tags,
		MaxBufferedSpans:            maxBufferedSpans,
		GRPCMaxCallSendMsgSizeBytes: grpcMaxMsgSize,
		ReportingPeriod:             maxReportingPeriod,
		MinReportingPeriod:          minReportingPeriod,
		Propagators:                 propagators,
	}

	r.tracer = lightstep.NewTracer(opts)

	quit := make(chan struct{})

	go func() {
		time.Sleep(stopAfter)
		quit <- struct{}{}
	}()

	ticker := time.NewTicker(genInterval)
	for {
		select {
		case <-quit:
			log.Info("quit")
			return
		case <-ticker.C:
			r.createSpans(genSpans, genSpanSize)
		}
	}
}

func createEventLogger() lightstep.EventHandler {
	return func(event lightstep.Event) {
		if e, ok := event.(lightstep.ErrorEvent); ok {
			log.WithError(e).Warn("LightStep tracer received an error event")
		} else if e, ok := event.(lightstep.EventStatusReport); ok {
			log.WithFields(log.Fields{
				"duration":      e.Duration(),
				"sent_spans":    e.SentSpans(),
				"dropped_spans": e.DroppedSpans(),
			}).Debugf("Sent a report to the collectors")
		} else if _, ok := event.(lightstep.EventTracerDisabled); ok {
			log.Warn("LightStep tracer has been disabled")
		}
	}
}
