package main

import (
	"os"

	"github.com/bradfitz/gomemcache/memcache"
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

	p := imageproxy.NewProxy(nil, nil)

	e.GET("/image/*", func(c echo.Context) error {
		c.Request().URL.Path = "/" + c.Param("*")
		return echo.WrapHandler(p)(c)
	})

	e.GET("/summary", SummaryHandler)

	e.Logger.Fatal(e.Start(":8082"))
}
