package main

import (
	"os"

	"github.com/bradfitz/gomemcache/memcache"
	"github.com/gregjones/httpcache/diskcache"
	"github.com/labstack/echo"
	"willnorris.com/go/imageproxy"
)

var (
	mc *memcache.Client
)

func main() {

	mc = memcache.New(os.Getenv("MEMCACHED_HOST"))
	defer mc.Close()

	e := echo.New()

	diskCache := diskcache.New("/tmp/hyperproxy")
	p := imageproxy.NewProxy(nil, diskCache)

	e.GET("/image/*", func(c echo.Context) error {
		c.Request().URL.Path = "/" + c.Param("*")
		return echo.WrapHandler(p)(c)
	})

	e.GET("/summary", SummaryHandler)

	e.Logger.Fatal(e.Start(":8082"))
}
