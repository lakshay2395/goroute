package main

import (
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"

	"github.com/bradfitz/gomemcache/memcache"
	"github.com/gorilla/mux"
)

var configPath = flag.String("config", "./goroute.json", "Config File For Router")
var envFilePath = flag.String("env", "NONE", "Environment variables list file path")

var tLSVersion = map[string]uint16{
	"TLS 1.0": tls.VersionTLS10,
	"TLS 1.1": tls.VersionTLS11,
	"TLS 1.2": tls.VersionTLS12,
}

func main() {
	flag.Parse()
	data, err := ioutil.ReadFile(*configPath)
	jsonContent := string(data)
	if err != nil {
		panic(fmt.Sprintf("Reading config file failed : %s", err.Error()))
	}
	if *envFilePath != "NONE" {
		env := map[string]string{}
		envBytes, err := ioutil.ReadFile(*envFilePath)
		if err != nil {
			panic(fmt.Sprintf("Reading env file failed : %s", err.Error()))
		}
		err = json.Unmarshal(envBytes, &env)
		if err != nil {
			panic(fmt.Sprintf("Unable to unmarshal content from env file : %s", err.Error()))
		}
		for k, v := range env {
			environmentValue := os.Getenv(v)
			jsonContent = strings.Replace(jsonContent, "$"+k+"$", environmentValue, -1)
		}
	}
	config := Config{}
	err = json.Unmarshal([]byte(jsonContent), &config)
	if err != nil {
		panic(fmt.Sprintf("Unable to unmarshal content from conf file : %s", err.Error()))
	}
	err = StartRouter(config)
	if err != nil {
		panic(fmt.Sprintf("Unable to start router : %s", err.Error()))
	}
}

//StartRouter - start router
func StartRouter(config Config) error {
	r := mux.NewRouter()
	r.StrictSlash(false)
	var cachingClient *memcache.Client
	if config.Cache.Enabled {
		cachingClient = GetCachingClient(config)
	}
	routes := config.Routes
	for _, route := range routes {
		if route.TargetType == "URL" {
			func(route Route) {
				r.PathPrefix(route.Path).HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					temp := route
					if strings.Split(req.RequestURI, "/")[1] != strings.Split(temp.Path, "/")[1] {
						http.NotFound(w, req)
					} else {
						headers := temp.Headers
						for k, v := range headers {
							req.Header.Add(k, v)
						}
						url, _ := url.Parse(temp.Target)
						proxy := httputil.NewSingleHostReverseProxy(url)
						req.URL.Host = url.Host
						req.URL.Scheme = url.Scheme
						req.URL.Path = req.URL.Path[len(route.Path):len(req.URL.Path)]
						req.Header.Set("X-Forwarded-Host", req.Header.Get("Host"))
						req.Host = url.Host
						proxy.ServeHTTP(w, req)
					}
				})
			}(route)
		} else {
			func(route Route) {
				if config.Cache.Enabled {
					r.PathPrefix(route.Path).Handler(http.StripPrefix(route.Path, FileServerWithCache(Dir(route.Target), cachingClient, route.CacheExpiry)))
				} else {
					r.PathPrefix(route.Path).Handler(http.StripPrefix(route.Path, http.FileServer(http.Dir(route.Target))))
				}
			}(route)
		}
	}
	log.Println("Starting server at " + config.Host + ":" + config.Port)
	if config.Security.Enabled {
		cfg := &tls.Config{
			MinVersion:         tLSVersion[config.Security.MinTLSVersion],
			MaxVersion:         tLSVersion[config.Security.MaxTLSVersion],
			InsecureSkipVerify: true,
		}
		srv := &http.Server{
			Addr:      config.Host + ":" + config.Port,
			Handler:   r,
			TLSConfig: cfg,
		}
		return srv.ListenAndServeTLS(config.Security.CertPath, config.Security.KeyPath)
	}
	return http.ListenAndServe(config.Host+":"+config.Port, r)
}

//GetCachingClient - get etcd caching client
func GetCachingClient(config Config) *memcache.Client {
	mc := memcache.New(config.Cache.EndPoint)
	return mc
}
