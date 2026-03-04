package health

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/kyma-project/kyma-environment-broker/internal/httputil"
)

type Server struct {
	Address string
	Log     *slog.Logger
}

func NewServer(host, port string, log *slog.Logger) *Server {
	return &Server{
		Address: fmt.Sprintf("%s:%s", host, port),
		Log:     log.With("server", "health"),
	}
}

func (srv *Server) ServeAsync() {
	healthRouter := httputil.NewRouter()
	healthRouter.HandleFunc("/healthz", livenessHandler())
	go func() {
		healthServer := &http.Server{
			Addr:              srv.Address,
			Handler:           healthRouter,
			ReadHeaderTimeout: 10 * time.Second,
		}
		err := healthServer.ListenAndServe()
		if err != nil {
			srv.Log.Error(fmt.Sprintf("HTTP Health server ListenAndServe: %v", err))
		}
	}()
}

func livenessHandler() func(w http.ResponseWriter, _ *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}
}
