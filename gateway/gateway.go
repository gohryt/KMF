package gateway

import (
	"bytes"
	"errors"
	"os"
	"strconv"
	"unsafe"

	"github.com/goccy/go-json"
	"github.com/valyala/fasthttp"

	"github.com/go-asphyxia/middlewares/CORS"
	"github.com/go-asphyxia/middlewares/HSTS"
)

type (
	Configuration struct {
		Name   string
		Scheme Scheme
	}

	Gateway struct {
		HSTS *HSTS.HSTS
		CORS *CORS.CORS

		Name   string
		Scheme Scheme
	}

	Scheme struct {
		Path     string
		Services map[string]*Service
	}

	Service struct {
		URLs []URL

		Methods map[string]Require
	}

	URL struct {
		client *fasthttp.HostClient

		Host string
		Port int
	}

	Require []string
)

func Create(configuration *Configuration, HSTS *HSTS.HSTS, CORS *CORS.CORS) (gateway *Gateway, err error) {
	file, err := os.Open(configuration.Scheme.Path)
	if err != nil {
		return
	}

	gateway = &Gateway{Name: configuration.Name, HSTS: HSTS, CORS: CORS}
	scheme := &gateway.Scheme

	err = json.NewDecoder(file).Decode(scheme)
	if err != nil {
		return
	}

	for i := range scheme.Services {
		for j := range scheme.Services[i].URLs {
			scheme.Services[i].URLs[j].client = &fasthttp.HostClient{
				Addr:                  scheme.Services[i].URLs[j].Host + ":" + strconv.Itoa(scheme.Services[i].URLs[j].Port),
				Name:                  configuration.Name,
				DialDualStack:         true,
				SecureErrorLogMessage: true,
			}
		}
	}

	return
}

func (gateway *Gateway) Find(service, method string) (client *fasthttp.HostClient, err error) {
	scheme := &gateway.Scheme

	s := scheme.Services[service]
	if s == nil {
		err = errors.New("service not found")
		return
	}

	m := s.Methods[method]
	if m == nil {
		err = errors.New("method not found")
		return
	}

	client = s.URLs[0].client
	return
}

func (gateway *Gateway) Proxy() func(*fasthttp.RequestCtx) {
	verifier := gateway.CORS.Verify()
	setter := gateway.CORS.Set()

	return func(rc *fasthttp.RequestCtx) {
		err := gateway.HSTS.Verify(rc)

		if err != nil {
			rc.SetStatusCode(fasthttp.StatusBadRequest)
			rc.SetBodyString(err.Error())
			return
		}

		err = verifier(rc)
		if err != nil {
			rc.SetStatusCode(fasthttp.StatusBadRequest)
			rc.SetBodyString(err.Error())
			return
		}

		req := &rc.Request
		uri := req.URI()

		service, path, err := parse(uri.Path())
		if err != nil {
			rc.SetStatusCode(fasthttp.StatusBadRequest)
			rc.SetBodyString(err.Error())
			return
		}

		s, p := unsafe.String(unsafe.SliceData(service), len(service)), unsafe.String(unsafe.SliceData(path), len(path))

		client, err := gateway.Find(s, p)
		if err != nil {
			rc.SetStatusCode(fasthttp.StatusBadRequest)
			rc.SetBodyString(err.Error())
			return
		}

		uri.SetScheme("http")
		uri.SetPath(p)

		err = client.Do(req, &rc.Response)
		if err != nil {
			rc.SetStatusCode(fasthttp.StatusBadRequest)
			rc.SetBodyString(err.Error())
			return
		}

		gateway.HSTS.Set(rc)
		setter(rc)
	}

}

func parse(path []byte) ([]byte, []byte, error) {
	length := len(path)

	if length < 4 {
		return nil, nil, errors.New("request path is too short")
	}

	path = path[1:]

	index := bytes.IndexByte(path, '/')
	slash := index + 1

	if index < 0 || length < slash {
		return nil, nil, errors.New("request should contain both service and method in path")
	}

	return path[:index], path[index:], nil
}
