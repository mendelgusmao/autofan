package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"time"

	"github.com/md14454/gosensors"
	"gopkg.in/yaml.v2"
)

type configSpec struct {
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
		config     = &configSpec{}
	)

	content, err := ioutil.ReadFile(configFile)

	if err != nil {
		fmt.Println("reading config file: ", err)
		os.Exit(1)
	}

	if err := yaml.Unmarshal(content, config); err != nil {
		fmt.Println("reading yaml:", err)
		os.Exit(1)
	}

	for _, sensor := range config.Sensors {
		re, err := regexp.Compile(sensor)

		if err != nil {
			fmt.Printf("build regex (%v): %v\n", sensor, err)
			os.Exit(1)
		}

		config.sensors = append(config.sensors, re)
	}

	interval, err := time.ParseDuration(config.Interval)

	if err != nil {
		fmt.Println("parsing interval: ", err)
		os.Exit(1)
	}

	config.interval = interval

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)

	work(config, sig)
}

func work(config *configSpec, sig chan os.Signal) {
	gosensors.Init()
	defer gosensors.Cleanup()

	ticker := time.NewTicker(config.interval)
	lastMeanTemperature := 0.0

	go func() {
		for range ticker.C {
			temperatures, fanSpeed, err := fetchValues(config.Fan, config.sensors)

			if err != nil {
				fmt.Println("fetching temperatures:", err)
				continue
			}

			if len(temperatures) == 0 {
				fmt.Println("got no temperature values. check your configuration")
				continue
			}

			meanTemperature, newFanSpeed := computeNewFanSpeed(config, temperatures)

			if meanTemperature == lastMeanTemperature {
				continue
			}

			if err := ioutil.WriteFile(config.Output, []byte(strconv.Itoa(newFanSpeed)), 0644); err != nil {
				fmt.Println("setting fan speed:", err)
				continue
			}

			fmt.Printf("%v -- mean:%0.2f -- from %d RPM to %d RPM\n", temperatures, meanTemperature, fanSpeed, newFanSpeed)

			lastMeanTemperature = meanTemperature
		}
	}()

	for {
		select {
		case <-sig:
			fmt.Println("signal received. exiting...")
			return
		}
	}
}

func fetchValues(fanName string, sensorsRE []*regexp.Regexp) (sensorsValues, int, error) {
	temperatures := make(sensorsValues)
	fanSpeed := 0

	for _, chip := range gosensors.GetDetectedChips() {
		for _, feature := range chip.GetFeatures() {
			sensorName := chip.String() + ":" + feature.GetLabel()

			if sensorName == fanName {
				fanSpeed = int(feature.GetValue())
				continue
			}

			if len(sensorsRE) != 0 {
				ok := false

				for _, re := range sensorsRE {
					if re.MatchString(sensorName) {
						ok = true
						continue
					}
				}

				if !ok {
					continue
				}
			}

			temperatures[sensorName] = feature.GetValue()
		}
	}

	return temperatures, fanSpeed, nil
}

func computeNewFanSpeed(config *configSpec, values sensorsValues) (float64, int) {
	amount := float64(len(values))
	sum := 0.0

	for _, temperature := range values {
		sum += temperature
	}

	meanTemperature := sum / amount

	return meanTemperature, int(
		float64(config.MinSpeed) +
			(float64(config.MaxSpeed-config.MinSpeed) /
				(config.HighTemp - config.NormalTemp) *
				(meanTemperature - config.NormalTemp)),
	)
}
