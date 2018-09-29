// Craig Hesling
// November 12, 2017
//
// This is an OpenChirp service that makes an http request when a certain conditions are met.
package main

import (
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/Knetic/govaluate"

	"github.com/openchirp/framework"
	log "github.com/sirupsen/logrus"
	"github.com/urfave/cli"
)

const (
	version string = "1.0"
)

const (
	configExpression = "expr"
	configValue      = "value"
	configMethod     = "method"
	configUri        = "uri"
)

const (
	// Set this value to true to have the service publish a service status of
	// "Running" each time it receives a device update event
	//
	// This could be used as a service alive pulse if enabled
	// Otherwise, the service status will indicate "Started" at the time the
	// service "Started" the client
	runningStatus = true
)

type Device struct {
	expr   *govaluate.EvaluableExpression
	value  *govaluate.EvaluableExpression
	uri    string
	values map[string]interface{}
}

func NewDevice() framework.Device {
	d := &Device{}
	d.ResetValues()
	return framework.Device(d)
}

func (d *Device) ResetValues() {
	d.values = make(map[string]interface{})
}

func (d *Device) ProcessLink(ctrl *framework.DeviceControl) string {
	logitem := log.WithField("deviceid", ctrl.Id())
	logitem.Debug("Linking with config:", ctrl.Config())

	uri := ctrl.Config()[configUri]
	exprStr := ctrl.Config()[configExpression]
	valueStr := ctrl.Config()[configValue]

	expr, err := govaluate.NewEvaluableExpression(exprStr)
	if err != nil {
		logitem.Warnf("Error parsing expr: %v", err)
		return fmt.Sprint(err)
	}
	value, err := govaluate.NewEvaluableExpression(valueStr)
	if err != nil {
		logitem.Warnf("Error parsing value: %v", err)
		return fmt.Sprint(err)
	}

	d.uri = uri
	d.expr = expr
	d.value = value

	for _, v := range d.expr.Vars() {
		subtopic := "transducer/" + v
		ctrl.Subscribe(subtopic, v)
	}

	// for _, v := range d.value.Vars() {
	// 	subtopic := "transducer/" + v
	// 	ctrl.Subscribe(subtopic, -1)
	// }

	return "Success"
}
func (d *Device) ProcessUnlink(ctrl *framework.DeviceControl) {
	logitem := log.WithField("deviceid", ctrl.Id())
	logitem.Debug("Unlinked:")
}
func (d *Device) ProcessConfigChange(ctrl *framework.DeviceControl, cchanges, coriginal map[string]string) (string, bool) {
	logitem := log.WithField("deviceid", ctrl.Id())
	logitem.Debug("Processing Config Change:", cchanges)
	return "", false
}
func (d *Device) ProcessMessage(ctrl *framework.DeviceControl, msg framework.Message) {
	logitem := log.WithField("deviceid", ctrl.Id())
	logitem.Debugf("Processing Message: %v: [ % #x ]", msg.Key(), msg.Payload())

	value, err := strconv.ParseFloat(string(msg.Payload()), 64)
	if err != nil {
		log.Warnf("Failed to parse a float64 from %v: %v", string(msg.Payload()), err)
		ctrl.Publish("transducer/err", fmt.Sprint(err))
		return
	}
	d.values[msg.Key().(string)] = value

	exprResult, err := d.expr.Evaluate(d.values)
	if err != nil {
		log.Warnf("Failed to evaluate %v with %v", d.value, d.values)
		ctrl.Publish("transducer/err", fmt.Sprint(err))
		return
	}

	if result, ok := exprResult.(bool); result && ok {
		valueResult, err := d.value.Evaluate(d.values)
		if err != nil {
			log.Warnf("Failed to evaluate %v with %v", d.value, d.values)
			ctrl.Publish("transducer/err", fmt.Sprint(err))
			return
		}
		log.Debugf("Evaluated %v with %v = %v", d.value, d.values, valueResult)
		ctrl.Publish("transducer/out", fmt.Sprint(valueResult))
		// send POST request
		if len(d.uri) > 0 {
			log.Debug("Sending POST request")
			req, err := http.NewRequest("POST", d.uri, strings.NewReader(fmt.Sprint(valueResult)))
			if err != nil {
				log.Warnf("Failed to send POST to %v with value %v", d.uri, valueResult)
				ctrl.Publish("transducer/err", fmt.Sprint(err))
				return
			}
			c := &http.Client{}
			resp, err := c.Do(req)
			if err != nil {
				log.Warnf("Failed to send POST to %v with value %v", d.uri, valueResult)
				ctrl.Publish("transducer/err", fmt.Sprint(err))
				return
			}
			defer resp.Body.Close()

		}
	} else {
		log.Debugf("Did not evaluate value because result=%v and ok=%v", result, ok)
	}
}

func run(ctx *cli.Context) error {
	/* Set logging level */
	log.SetLevel(log.Level(uint32(ctx.Int("log-level"))))

	log.Info("Starting Example Service")

	/* Start framework service client */
	c, err := framework.StartServiceClientManaged(
		ctx.String("framework-server"),
		ctx.String("mqtt-server"),
		ctx.String("service-id"),
		ctx.String("service-token"),
		"Unexpected disconnect!",
		NewDevice)
	if err != nil {
		log.Error("Failed to StartServiceClient: ", err)
		return cli.NewExitError(nil, 1)
	}
	defer c.StopClient()
	log.Info("Started service")

	/* Post service status indicating I am starting */
	err = c.SetStatus("Starting")
	if err != nil {
		log.Error("Failed to publish service status: ", err)
		return cli.NewExitError(nil, 1)
	}
	log.Info("Published Service Status")

	/* Setup signal channel */
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)

	/* Post service status indicating I started */
	err = c.SetStatus("Started")
	if err != nil {
		log.Error("Failed to publish service status: ", err)
		return cli.NewExitError(nil, 1)
	}
	log.Info("Published Service Status")

	for {
		select {
		case sig := <-signals:
			log.WithField("signal", sig).Info("Received signal")
			goto cleanup
		}
	}

cleanup:

	log.Warning("Shutting down")
	err = c.SetStatus("Shutting down")
	if err != nil {
		log.Error("Failed to publish service status: ", err)
	}
	log.Info("Published service status")

	return nil
}

func main() {
	app := cli.NewApp()
	app.Name = "example-service"
	app.Usage = ""
	app.Copyright = "See https://github.com/openchirp/example-service for copyright information"
	app.Version = version
	app.Action = run
	app.Flags = []cli.Flag{
		cli.StringFlag{
			Name:   "framework-server",
			Usage:  "OpenChirp framework server's URI",
			Value:  "http://localhost:7000",
			EnvVar: "FRAMEWORK_SERVER",
		},
		cli.StringFlag{
			Name:   "mqtt-server",
			Usage:  "MQTT server's URI (e.g. scheme://host:port where scheme is tcp or tls)",
			Value:  "tls://localhost:1883",
			EnvVar: "MQTT_SERVER",
		},
		cli.StringFlag{
			Name:   "service-id",
			Usage:  "OpenChirp service id",
			EnvVar: "SERVICE_ID",
		},
		cli.StringFlag{
			Name:   "service-token",
			Usage:  "OpenChirp service token",
			EnvVar: "SERVICE_TOKEN",
		},
		cli.IntFlag{
			Name:   "log-level",
			Value:  4,
			Usage:  "debug=5, info=4, warning=3, error=2, fatal=1, panic=0",
			EnvVar: "LOG_LEVEL",
		},
	}
	app.Run(os.Args)
}
