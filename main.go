package main

import (
	"os"

	"net/http"
	"time"

	"github.com/bradfitz/gomemcache/memcache"
	"github.com/labstack/echo-contrib/echoprometheus"
	"github.com/labstack/echo-contrib/pprof"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/prometheus/client_golang/prometheus"
)

var denyIps = []string{
	"127.0.0.0/8",
	"10.0.0.0/8",
	"172.16.0.0/12",
	"192.168.0.0/16",
	"::1/128",
	"fc00::/7",
}

var (
	mc     *memcache.Client
	client = &http.Client{
		Timeout: 10 * time.Second,
	}
)

const (
	useragent = "hyperproxy bot"
)

func main() {

	mc = memcache.New(os.Getenv("MEMCACHED_HOST"))
	defer mc.Close()

	e := echo.New()
	pprof.Register(e)
	e.Use(middleware.Recover())

	e.Use(echoprometheus.NewMiddlewareWithConfig(echoprometheus.MiddlewareConfig{
		Namespace: "hyperproxy",
		LabelFuncs: map[string]echoprometheus.LabelValueFunc{
			"url": func(c echo.Context, err error) string {
				return "REDACTED"
			},
		},
		Skipper: func(c echo.Context) bool {
			return c.Path() == "/metrics" || c.Path() == "/health"
		},
	}))

	e.GET("/image/*", ImageHandler)
	e.GET("/summary", SummaryHandler)

	var currentCacheSizeMetrics = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "hyperproxy_image_cache_size",
		Help: "Current size of the image cache",
	})
	prometheus.MustRegister(currentCacheSizeMetrics)

	go func() {
		totalsize := CleanDiskCache()
		currentCacheSizeMetrics.Set(float64(totalsize))

		ticker := time.NewTicker(5 * time.Minute)
		for {
			select {
			case <-ticker.C:
				totalsize := CleanDiskCache()
				currentCacheSizeMetrics.Set(float64(totalsize))
			}
		}
	}()

	e.GET("/metrics", echoprometheus.NewHandler())

	PORT := os.Getenv("PORT")
	if PORT == "" {
		PORT = "8080"
	}

	e.Logger.Fatal(e.Start(":" + PORT))
}
