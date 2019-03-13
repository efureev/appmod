package appmod

import (
	"errors"
	"testing"
)
import . "github.com/smartystreets/goconvey/convey"

func TestAppModuleConfig(t *testing.T) {
	Convey("AppModuleConfig", t, func() {
		config := Config{name: `test name`, version: `v1`}

		Convey("Base", func() {
			So(config, ShouldHaveSameTypeAs, Config{})
		})

		Convey("Name", func() {
			name := config.Name()
			So(name, ShouldEqual, "test name")
		})
		Convey("Version", func() {
			v := config.Version()
			So(v, ShouldEqual, "v1")
		})

		Convey("Default", func() {
			c := DefaultConfig()
			So(c, ShouldHaveSameTypeAs, Config{})

			Convey("Name", func() {
				name := c.Name()
				So(name, ShouldEqual, "App Module")
			})
			Convey("Version", func() {
				v := c.Version()
				So(v, ShouldEqual, "v0.0.1")
			})

		})

	})
}

func TestAppModuleBaseAppMod(t *testing.T) {
	Convey("BaseAppModule", t, func() {
		config := Config{name: `test app`, version: `v1`}
		mod := BaseAppModule{config: config}

		Convey("Base", func() {
			So(mod, ShouldHaveSameTypeAs, BaseAppModule{})
		})

		Convey("Config", func() {
			So(mod.Config(), ShouldHaveSameTypeAs, Config{})
			So(mod.Config().Name(), ShouldEqual, "test app")
			So(mod.Config().Version(), ShouldEqual, "v1")

			newConfig := Config{name: `new test app`, version: `v2`}
			mod.SetConfig(newConfig)

			So(mod.Config(), ShouldHaveSameTypeAs, Config{})
			So(mod.Config().Name(), ShouldEqual, "new test app")
			So(mod.Config().Version(), ShouldEqual, "v2")
		})

		Convey("Init", func() {
			err := mod.Init()
			So(err, ShouldBeNil)
		})

		Convey("Destroy", func() {
			err := mod.Destroy()
			So(err, ShouldBeNil)
		})

		Convey("Events", func() {
			Convey("Normal fly", func() {
				newConfig := Config{name: `New Application`, version: `v3`}

				mod.BeforeStart(func(m AppModule) error {
					m.SetConfig(newConfig)
					return nil
				})

				finishConfig := Config{name: `New Application 2`, version: `v4`}

				mod.BeforeDestroy(func(m AppModule) error {
					m.SetConfig(finishConfig)
					return nil
				})

				err := mod.Init()
				So(err, ShouldBeNil)

				So(mod.Config(), ShouldHaveSameTypeAs, newConfig)
				So(mod.Config().Name(), ShouldEqual, newConfig.Name())
				So(mod.Config().Version(), ShouldEqual, newConfig.Version())

				err = mod.Destroy()
				So(err, ShouldBeNil)

				So(mod.Config(), ShouldHaveSameTypeAs, finishConfig)
				So(mod.Config().Name(), ShouldEqual, finishConfig.Name())
				So(mod.Config().Version(), ShouldEqual, finishConfig.Version())

			})

			Convey("Panic mode", func() {

				error1 := errors.New(`error BeforeStart`)
				error2 := errors.New(`error BeforeDestroy`)
				mod.BeforeStart(func(_ AppModule) error {
					return error1
				})

				mod.BeforeDestroy(func(_ AppModule) error {
					return error2
				})

				So(mod.Init(), ShouldBeError, error1)
				So(mod.Destroy(), ShouldBeError, error2)
			})

		})

	})
}
