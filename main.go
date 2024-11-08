package main

import (
	"os"

	"github.com/bradfitz/gomemcache/memcache"
	"github.com/gregjones/httpcache/diskcache"
	"github.com/labstack/echo"
	"github.com/labstack/echo/middleware"
	"willnorris.com/go/imageproxy"
)

var (
	mc *memcache.Client
)

func main() {

	mc = memcache.New(os.Getenv("MEMCACHED_HOST"))
	defer mc.Close()

	e := echo.New()
	e.Use(middleware.Recover())

	diskCache := diskcache.New("/tmp/hyperproxy")
	p := imageproxy.NewProxy(nil, diskCache)
	p.FollowRedirects = true
	p.ContentTypes = []string{"image/*"}
	p.DenyHosts = []string{
		"localhost",
		"127.0.0.0/8",
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"::1/128",
		"fc00::/7",
	}

	e.GET("/image/*", func(c echo.Context) error {
		c.Request().URL.Path = "/" + c.Param("*")
		return echo.WrapHandler(p)(c)
	})

	e.GET("/summary", SummaryHandler)

	PORT := os.Getenv("PORT")
	if PORT == "" {
		PORT = "8080"
	}

	e.Logger.Fatal(e.Start(":" + PORT))
}
