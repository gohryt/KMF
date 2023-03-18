package main

import (
	"context"
	"crypto/tls"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"gitlab.com/gohryt/KMF/gateway"

	"github.com/sakirsensoy/genv"
	"github.com/sakirsensoy/genv/dotenv"
	"github.com/valyala/fasthttp"

	"github.com/go-asphyxia/middlewares/CORS"
	"github.com/go-asphyxia/middlewares/HSTS"

	atls "github.com/go-asphyxia/tls"
)

type (
	Configuration struct {
		Host  string
		Email string

		HSTS    *HSTS.Configuration
		Gateway *gateway.Configuration
	}

	Mapping struct {
		Services map[string]Service
	}

	Service struct {
		Addresses []string
		Methods   map[string]Method
	}

	Method struct {
		Require []string
	}
)

var (
	envfile string = ".env"
)

func main() {
	if len(os.Args) > 1 {
		envfile = os.Args[1]
	}

	dotenv.Load(envfile)

	c := &Configuration{
		Host:  genv.Key("HOST").String(),
		Email: genv.Key("EMAIL").String(),
		HSTS: &HSTS.Configuration{
			MaxAge: genv.Key("HSTS_MAX_AGE").Default(31536000).Int(),
		},
		Gateway: &gateway.Configuration{
			Name: genv.Key("GATEWAY_NAME").Default("gateway").String(),
			Scheme: gateway.Scheme{
				Path: genv.Key("GATEWAY_SCHEME_PATH").Default(".scheme").String(),
			},
		},
	}

	shutdown, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	err := Main(shutdown, c)
	if err != nil {
		panic(err)
	}
}

func Main(shutdown context.Context, configuration *Configuration) error {
	HSTS := HSTS.NewHSTS(configuration.HSTS)
	CORS := CORS.NewCORS(&CORS.Configuration{
		Origins: []string{configuration.Host},
		Methods: []string{fasthttp.MethodGet, fasthttp.MethodPost, fasthttp.MethodPut, fasthttp.MethodDelete, fasthttp.MethodOptions},
		Headers: []string{fasthttp.HeaderContentType, fasthttp.HeaderAccept, fasthttp.HeaderAuthorization},
	})

	gateway, err := gateway.Create(configuration.Gateway, HSTS, CORS)
	if err != nil {
		return err
	}

	t, err := atls.NewTLS(atls.Version12)
	if err != nil {
		return err
	}

	tlsConfiguration, err := t.Auto(configuration.Email, atls.DefaultCertificatesCachePath, configuration.Host, ("www." + configuration.Host))
	if err != nil {
		return err
	}

	http, err := net.Listen("tcp", ":80")
	if err != nil {
		return err
	}

	https, err := net.Listen("tcp", ":443")
	if err != nil {
		return err
	}

	https = tls.NewListener(https, tlsConfiguration)

	s := &fasthttp.Server{
		Name:    configuration.Host,
		Handler: gateway.Proxy(),

		Concurrency:  1024 * 16,
		ReadTimeout:  4 * time.Second,
		WriteTimeout: 4 * time.Second,
		IdleTimeout:  16 * time.Second,
		TCPKeepalive: true,

		KeepHijackedConns: true,
		StreamRequestBody: true,
		CloseOnShutdown:   true,
	}
	defer s.Shutdown()

	errors := make(chan error)

	go func() {
		errors <- s.Serve(http)
	}()

	go func() {
		errors <- s.Serve(https)
	}()

	select {
	case <-shutdown.Done():
		return nil
	case err = <-errors:
		return err
	}
}
