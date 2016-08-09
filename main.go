package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/md14454/gosensors"
	"gopkg.in/yaml.v2"
)

type Autofan struct {
	Mode       string   `yaml:"mode"`
	Interval   string   `yaml:"interval"`
	MinSpeed   int64    `yaml:"minSpeed"`
	MaxSpeed   int64    `yaml:"maxSpeed"`
	HighTemp   float64  `yaml:"highTemp"`
	NormalTemp float64  `yaml:"normalTemp"`
	Fan        string   `yaml:"fan"`
	Output     string   `yaml:"output"`
	Sensors    []string `yaml:"sensors"`
	sensors    []*regexp.Regexp
	interval   time.Duration
}

type sensorsValues map[string]float64

func main() {
	var (
		configFile = os.Getenv("HOME") + "/.autofan"
		autofan    = &Autofan{
			Mode:       "mean",
			Interval:   "3s",
			MinSpeed:   1500,
			MaxSpeed:   5000,
			HighTemp:   70,
			NormalTemp: 40,
			Fan:        "applesmc-isa-0300:Master",
			Output:     "/sys/devices/platform/applesmc.768/fan1_output",
			Sensors:    []string{"coretemp-isa-0000:Core .*"},
		}
	)

	if err := autofan.configure(configFile); err != nil {
		fmt.Println("configuring:", err)
		os.Exit(1)
	}

	autofan.work()
}

func (a *Autofan) configure(configFile string) error {
	content, err := ioutil.ReadFile(configFile)

	if err != nil {
		return fmt.Errorf("reading config file: %s", err)
	}

	if err := yaml.Unmarshal(content, a); err != nil {
		return fmt.Errorf("reading yaml: %s", err)
	}

	for _, sensor := range a.Sensors {
		re, err := regexp.Compile(sensor)

		if err != nil {
			return fmt.Errorf("build regex (%v): %v\n", sensor, err)
		}

		a.sensors = append(a.sensors, re)
	}

	interval, err := time.ParseDuration(a.Interval)

	if err != nil {
		return fmt.Errorf("parsing interval: %s", err)
	}

	a.interval = interval

	return nil
}

func (a *Autofan) work() {
	gosensors.Init()
	defer gosensors.Cleanup()

	ticker := time.NewTicker(a.interval)
	lastTemperature := 0.0

	go func() {
		for range ticker.C {
			temperatures, fanSpeed := a.fetchValues()

			if len(temperatures) == 0 {
				fmt.Println("got no temperature values. check your configuration")
				continue
			}

			temperature, newFanSpeed, err := a.computeNewFanSpeed(temperatures)

			if err != nil {
				fmt.Println(err)
				continue
			}

			if temperature == lastTemperature {
				continue
			}

			if err := ioutil.WriteFile(a.Output, []byte(strconv.Itoa(newFanSpeed)), 0644); err != nil {
				fmt.Println("setting fan speed:", err)
				continue
			}

			fmt.Printf("%v -- mean:%0.1f -- from %d RPM to %d RPM\n", temperatures, temperature, fanSpeed, newFanSpeed)

			lastTemperature = temperature
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)

	<-sig
	fmt.Println("signal received. exiting...")
}

func (a *Autofan) fetchValues() (sensorsValues, int) {
	temperatures := make(sensorsValues)
	fanSpeed := 0

	for _, chip := range gosensors.GetDetectedChips() {
		for _, feature := range chip.GetFeatures() {
			sensorName := chip.String() + ":" + feature.GetLabel()

			if strings.TrimSpace(sensorName) == a.Fan {
				fanSpeed = int(feature.GetValue())
				continue
			}

			if len(a.sensors) != 0 {
				ok := false

				for _, re := range a.sensors {
					if re.MatchString(sensorName) {
						ok = true
						break
					}
				}

				if !ok {
					continue
				}
			}

			temperatures[sensorName] = feature.GetValue()
		}
	}

	return temperatures, fanSpeed
}

func (a *Autofan) computeNewFanSpeed(values sensorsValues) (float64, int, error) {
	var sum, max, temp float64

	for _, temperature := range values {
		sum += temperature

		if temperature > max {
			max = temperature
		}
	}

	switch a.Mode {
	case "mean":
		temp = sum / float64(len(values))
	case "max":
		temp = max
	default:
		return 0, 0, fmt.Errorf("unrecognized mode '%s'. should be 'max' or 'mean'", a.Mode)
	}

	return temp, int(
		float64(a.MinSpeed) +
			(float64(a.MaxSpeed-a.MinSpeed) /
				(a.HighTemp - a.NormalTemp) *
				(temp - a.NormalTemp)),
	), nil
}
