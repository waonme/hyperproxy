package main

import (
	"github.com/labstack/echo"
	"willnorris.com/go/imageproxy"
)

func main() {
	e := echo.New()

	p := imageproxy.NewProxy(nil, nil)

	e.GET("/image/*", func(c echo.Context) error {
		c.Request().URL.Path = "/" + c.Param("*")
		return echo.WrapHandler(p)(c)
	})

	e.GET("/summary", SummaryHandler)

	e.Logger.Fatal(e.Start(":8082"))
}
