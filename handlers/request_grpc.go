package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/nicholasjackson/fake-service/client"
	"github.com/nicholasjackson/fake-service/errors"
	"github.com/nicholasjackson/fake-service/grpc/api"
	"github.com/nicholasjackson/fake-service/load"
	"github.com/nicholasjackson/fake-service/logging"
	"github.com/nicholasjackson/fake-service/response"
	"github.com/nicholasjackson/fake-service/timing"
	"github.com/nicholasjackson/fake-service/worker"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// FakeServer implements the gRPC interface
type FakeServer struct {
	name          string
	message       string
	duration      *timing.RequestDuration
	upstreamURIs  []string
	workerCount   int
	defaultClient client.HTTP
	grpcClients   map[string]client.GRPC
	errorInjector *errors.Injector
	loadGenerator *load.Generator
	log           *logging.Logger
}

// NewFakeServer creates a new instance of FakeServer
func NewFakeServer(
	name, message string,
	duration *timing.RequestDuration,
	upstreamURIs []string,
	workerCount int,
	defaultClient client.HTTP,
	grpcClients map[string]client.GRPC,
	i *errors.Injector,
	loadGenerator *load.Generator,
	l *logging.Logger,
) *FakeServer {

	return &FakeServer{
		name:          name,
		message:       message,
		duration:      duration,
		upstreamURIs:  upstreamURIs,
		workerCount:   workerCount,
		defaultClient: defaultClient,
		grpcClients:   grpcClients,
		errorInjector: i,
		loadGenerator: loadGenerator,
		log:           l,
	}
}

// Handle implements the FakeServer Handle interface method
func (f *FakeServer) Handle(ctx context.Context, in *api.Nil) (*api.Response, error) {

	// start timing the service this is used later for the total request time
	ts := time.Now()
	finished := f.loadGenerator.Generate()
	defer finished()

	hq := f.log.HandleGRPCRequest(ctx)
	defer hq.Finished()

	resp := &response.Response{}
	resp.Name = f.name
	resp.Type = "gRPC"
	resp.IPAddresses = getIPInfo()

	// are we injecting errors, if so return the error
	if er := f.errorInjector.Do(); er != nil {
		resp.Code = er.Code
		resp.Error = er.Error.Error()

		hq.SetError(er.Error)
		hq.SetMetadata("response", strconv.Itoa(er.Code))

		// encode the response into the gRPC error message
		s := status.New(codes.Code(resp.Code), er.Error.Error())
		s, _ = s.WithDetails(&api.Response{Message: resp.ToJSON()})

		// return the error
		return nil, s.Err()
	}

	// if we need to create upstream requests create a worker pool
	var upstreamError error
	if len(f.upstreamURIs) > 0 {
		wp := worker.New(f.workerCount, func(uri string) (*response.Response, error) {
			if strings.HasPrefix(uri, "http://") {
				return workerHTTP(hq.Span.Context(), uri, f.defaultClient, nil, f.log)
			}

			return workerGRPC(hq.Span.Context(), uri, f.grpcClients, f.log)
		})

		err := wp.Do(f.upstreamURIs)

		if err != nil {
			upstreamError = err
		}

		for _, v := range wp.Responses() {
			resp.AppendUpstream(v.URI, *v.Response)
		}
	}

	// service time is equal to the randomized time - the current time take
	d := f.duration.Calculate()
	et := time.Since(ts)
	rd := d - et

	if upstreamError != nil {
		resp.Code = int(codes.Internal)
		resp.Error = upstreamError.Error()

		hq.SetMetadata("response", strconv.Itoa(resp.Code))
		hq.SetError(upstreamError)

		// encode the response into the gRPC error message
		s := status.New(codes.Code(resp.Code), upstreamError.Error())
		s, _ = s.WithDetails(&api.Response{Message: resp.ToJSON()})

		return nil, s.Err()
	}

	// randomize the time the request takes
	lp := f.log.SleepService(hq.Span, rd)

	if rd > 0 {
		time.Sleep(rd)
	}

	lp.Finished()

	// log response code
	hq.SetMetadata("response", "0")

	// Calculate total elapsed time including duration
	te := time.Now()
	et = te.Sub(ts)

	resp.StartTime = ts.Format(timeFormat)
	resp.EndTime = te.Format(timeFormat)
	resp.Duration = et.String()

	// add the response body if there is no upstream error
	if upstreamError == nil {
		if strings.HasPrefix(f.message, "{") {
			resp.Body = json.RawMessage(f.message)
		} else {
			resp.Body = json.RawMessage(fmt.Sprintf(`"%s"`, f.message))
		}
	}

	return &api.Response{Message: resp.ToJSON()}, nil
}
