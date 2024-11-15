package main

import (
	"os"

	"net/http"
	"time"

	"github.com/bradfitz/gomemcache/memcache"
	"github.com/labstack/echo-contrib/pprof"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
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
	client = &http.Client{}
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

	e.GET("/image/*", ImageHandler)
	e.GET("/summary", SummaryHandler)

	go func() {
		ticker := time.NewTicker(5 * time.Second)
		for {
			select {
			case <-ticker.C:
				CleanDiskCache()
			}
		}
	}()

	PORT := os.Getenv("PORT")
	if PORT == "" {
		PORT = "8080"
	}

	e.Logger.Fatal(e.Start(":" + PORT))
}
