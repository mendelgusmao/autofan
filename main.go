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

type configSpec struct {
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
		config     = &configSpec{
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

	work(config)
}

func work(config *configSpec) {
	gosensors.Init()
	defer gosensors.Cleanup()

	ticker := time.NewTicker(config.interval)
	lastTemperature := 0.0

	go func() {
		for range ticker.C {
			temperatures, fanSpeed := fetchValues(config.Fan, config.sensors)

			if len(temperatures) == 0 {
				fmt.Println("got no temperature values. check your configuration")
				continue
			}

			temperature, newFanSpeed, err := computeNewFanSpeed(config, temperatures)

			if err != nil {
				fmt.Println(err)
				continue
			}

			if temperature == lastTemperature {
				continue
			}

			if err := ioutil.WriteFile(config.Output, []byte(strconv.Itoa(newFanSpeed)), 0644); err != nil {
				fmt.Println("setting fan speed:", err)
				continue
			}

			fmt.Printf("%v -- mean:%0.1f -- from %d RPM to %d RPM\n", temperatures, temperature, fanSpeed, newFanSpeed)

			lastTemperature = temperature
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)

	for {
		select {
		case <-sig:
			fmt.Println("signal received. exiting...")
			return
		}
	}
}

func fetchValues(fanName string, sensorsRE []*regexp.Regexp) (sensorsValues, int) {
	temperatures := make(sensorsValues)
	fanSpeed := 0

	for _, chip := range gosensors.GetDetectedChips() {
		for _, feature := range chip.GetFeatures() {
			sensorName := chip.String() + ":" + feature.GetLabel()

			if strings.TrimSpace(sensorName) == fanName {
				fanSpeed = int(feature.GetValue())
				continue
			}

			if len(sensorsRE) != 0 {
				ok := false

				for _, re := range sensorsRE {
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

func computeNewFanSpeed(config *configSpec, values sensorsValues) (float64, int, error) {
	var sum, max, temp float64

	for _, temperature := range values {
		sum += temperature

		if temperature > max {
			max = temperature
		}
	}

	switch config.Mode {
	case "mean":
		temp = sum / float64(len(values))
	case "max":
		temp = max
	default:
		return 0, 0, fmt.Errorf("unrecognized mode '%s'. should be 'max' or 'mean'", config.Mode)
	}

	return temp, int(
		float64(config.MinSpeed) +
			(float64(config.MaxSpeed-config.MinSpeed) /
				(config.HighTemp - config.NormalTemp) *
				(temp - config.NormalTemp)),
	), nil
}
