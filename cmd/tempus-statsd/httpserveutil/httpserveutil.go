package httpserveutil

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

func BadRequest(w http.ResponseWriter, format string, a ...any) error {
	return writeError(w, http.StatusBadRequest, format, a...)
}

func NotFound(w http.ResponseWriter, format string, a ...any) error {
	return writeError(w, http.StatusNotFound, format, a...)
}

func writeError(w http.ResponseWriter, code int, format string, a ...any) error {
	err := fmt.Errorf(format, a...)
	http.Error(w, err.Error(), code)
	return err
}

func InternalError(w http.ResponseWriter, format string, a ...any) error {
	err := fmt.Errorf(format, a...)
	writeError(w, http.StatusInternalServerError, "internal error")
	return err
}

func Unauthorized(w http.ResponseWriter, format string, a ...any) error {
	return writeError(w, http.StatusUnauthorized, format, a...)
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (rec *statusRecorder) WriteHeader(status int) {
	rec.status = status
	rec.ResponseWriter.WriteHeader(status)
}

type ErrorHandlerFunc func(w http.ResponseWriter, r *http.Request) error

func Handle(out io.Writer, f ErrorHandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		start := time.Now()
		err := f(rec, r)
		elapsed := time.Since(start)

		remoteaddr := r.Header.Get("X-Forwarded-For")
		if remoteaddr == "" {
			remoteaddr = r.RemoteAddr
		}

		if err != nil {
			fmt.Fprintf(out, "%s %s %s %s %d %s %s\n", start, remoteaddr, r.Method, r.RequestURI, rec.status, elapsed, err)
			return
		}

		fmt.Fprintf(out, "%s %s %s %s %d %s\n", start, remoteaddr, r.Method, r.RequestURI, rec.status, elapsed)
	}
}

func WriteJSON(w http.ResponseWriter, status int, data any) error {
	body, err := json.MarshalIndent(data, "", "\t")
	if err != nil {
		return fmt.Errorf("marshal data: %w", err)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(append(body, '\n'))

	return nil
}

func NewServer(addr string, mux http.Handler, tlsconf *tls.Config) *Server {
	return &Server{
		Server: &http.Server{
			Addr:              addr,
			Handler:           mux,
			TLSConfig:         tlsconf,
			ReadTimeout:       0,
			ReadHeaderTimeout: 0,
			WriteTimeout:      0,
			IdleTimeout:       0,
			MaxHeaderBytes:    0,
		},
	}
}

type Server struct {
	*http.Server
}

func (s *Server) Shutdown() error {
	d := 30 * time.Second
	timeout, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()

	// ensure the http server is shutdown
	if err := s.Server.Shutdown(timeout); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}

	return nil

}

func (s *Server) Run(context.Context) error {
	fmt.Printf("listening on tcp at %s\n", s.Addr)

	listener, err := net.Listen("tcp", s.Addr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}

	if s.Server.TLSConfig != nil {
		fmt.Printf("serving HTTPS at %s\n", listener.Addr())

		if err := s.Server.ServeTLS(listener, "", ""); err != http.ErrServerClosed {
			return fmt.Errorf("serve tls: %w", err)
		}
	} else {
		fmt.Printf("serving HTTP at %s\n", listener.Addr())

		if err := s.Server.Serve(listener); err != http.ErrServerClosed {
			return fmt.Errorf("serve: %w", err)
		}
	}

	return nil
}

type Router interface {
	Routes(out io.Writer) map[string]http.Handler
}

type Mux interface {
	Handle(path string, handler http.Handler)
}

func Register(mux Mux, out io.Writer, routers ...Router) {
	for _, router := range routers {
		routes := router.Routes(out)

		for path, handler := range routes {
			mux.Handle(path, handler)
		}
	}
}
