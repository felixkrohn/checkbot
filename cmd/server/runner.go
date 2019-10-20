package main

import (
	"bytes"
	"errors"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
)

// A channel to tell it to stop
var stopchan chan struct{}

// Starts a go routine for each check in the list.
func (app *application) startChecks() {

	log.Debug("Starting all checks now..")

	// Recreate the chan in case it was closed before
	stopchan = make(chan struct{})

	// Walk throught the check list
	for _, check := range app.checkList {
		// Only run the check if active
		if check.Active {
			go runCheck(check, stopchan)
		} else {
			log.Infof("Check %s not active", check.Name)
		}
	}
}

// Stop all running go routines.
func (app *application) stopChecks() {

	log.Debug("Stopping all checks now..")
	close(stopchan)

	// Walk throught the check list
	for _, check := range app.checkList {
		if check.Active {
			<-check.stoppedchan
		}
	}
	log.Debug("All checks are stopped.")
}

// Run the check and save the result to the list.
func runCheck(check Check, stopchan chan struct{}) {

	// Close the stoppedchan when this func exits
	defer close(check.stoppedchan)

	// Teardown
	defer func() {
		unregisterMetricsForCheck(&check)
	}()

	for {
		select {
		default:

			// Check if we can run the check
			if time.Now().Unix() > check.nextrun {

				log.Debugf("Running check %s", check.Name)

				// Unregister all metrics from the check
				unregisterMetricsForCheck(&check)

				// Run the script
				result, err := runBashScript(check)

				if err == nil {

					// Split the result from the check script, can be multiple lines
					resultLine := strings.Split(result, "\n")
					for _, line := range resultLine {
						if line != "" {
							// Extract values from the result and register the metric
							value, labels := convertResult(line)
							registerMetricsForCheck(&check, value, labels)
						}
					}
				} else {
					log.Warnf("Check %s failed with error: %s", check.Name, err)
				}

				// Set time for next run
				check.nextrun += int64(check.Interval)
			}

		case <-stopchan:
			// Stop
			log.Debugf("Stopping check %s", check.Name)
			return

		case <-time.After(10 * time.Second):
			// Task didn't stop in time
			log.Debugf("Forced stopping check %s", check.Name)
			return
		}

		// Slow down
		time.Sleep(1 * time.Second)
	}
}

// Register all metrics from Prometheus for a given check.
func registerMetricsForCheck(check *Check, value float64, labels map[string]string) {
	switch check.MetricType {
	case "Gauge":
		if check.metric == nil {
			check.metric = prometheus.NewGaugeVec(
				prometheus.GaugeOpts{
					Name: check.Name,
					Help: check.Help,
				},
				convertMapKeysToSlice(labels),
			)
			prometheus.MustRegister(check.metric.(*prometheus.GaugeVec))
		}
		check.metric.(*prometheus.GaugeVec).With(labels).Set(value)
	case "Counter":
		log.Warn("Metric type Counter not implemented yet!")
	case "Histogram":
		log.Warn("Metric type Counter not implemented yet!")
	case "Summary":
		log.Warn("Metric type Counter not implemented yet!")
	default:
		log.Warnf("Not able to register unknown metric type %s", check.MetricType)
		check.metric = nil
	}

	log.Tracef("Result from check %s -> value: %f, labels: %v", check.Name, value, labels)
}

// Unregister all metrics from Prometheus for a given check.
func unregisterMetricsForCheck(check *Check) {
	if check.metric != nil {
		switch check.MetricType {
		case "Gauge":
			prometheus.Unregister(check.metric.(*prometheus.GaugeVec))
		case "Counter":
			log.Warn("Metric type Counter not implemented yet!")
		case "Histogram":
			log.Warn("Metric type Counter not implemented yet!")
		case "Summary":
			log.Warn("Metric type Counter not implemented yet!")
		default:
			log.Warnf("Not able to unregister unknown metric type %s", check.MetricType)
		}
		check.metric = nil

		log.Debugf("Unregistered metrics for check %s", check.Name)
	}
}

// Run the check and return the result.
func runBashScript(check Check) (string, error) {

	log.Debugf("Execute shell script: %s", check.File)

	// Execute bash script
	cmd := exec.Command(determineBash(), check.File)
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	err := cmd.Run()

	scriptResult := out.String()
	scriptError := stderr.String()

	if err != nil {
		// Check failed with defined message
		if scriptResult != "" {
			log.Infof("Script %s failed with output: %v", check.File, scriptResult)
			return "", errors.New("Script failed with error: " + scriptResult)
		}

		// Check has error
		if scriptError != "" {
			log.Infof("Script %s failed with error: %v", check.File, scriptError)
			return "", errors.New("Script failed with error: " + scriptError)
		}

		// Execution failed
		log.Infof("Script %s finished with execution error: %v", check.File, err)
		return "", errors.New("Script failed with error: " + err.Error())
	}

	// Check run successfull
	log.Tracef("Script %s finished with success: %v", check.File, scriptResult)
	return scriptResult, nil
}

// Converts the return value from the script check.
// Format: value|label1:value1,label2:value2
func convertResult(result string) (float64, map[string]string) {
	var metricValue float64
	var labels = make(map[string]string)

	if strings.Contains(result, "|") {
		splitResult := strings.Split(result, "|")

		// Result of the check
		value := splitResult[0]

		// Labels of the check
		splitLabels := strings.Split(splitResult[1], ",")
		for _, label := range splitLabels {
			splitLabel := strings.SplitN(label, "=", 2)
			labels[splitLabel[0]] = splitLabel[1]
		}
		metricValue, _ = strconv.ParseFloat(value, 64)
	} else {
		metricValue, _ = strconv.ParseFloat(result, 64)
	}
	return metricValue, labels
}

// Convert the keys from a map to a slice.
func convertMapKeysToSlice(value map[string]string) []string {
	keys := make([]string, len(value))

	i := 0
	for k := range value {
		keys[i] = k
		i++
	}

	return keys
}

func determineBash() string {
	switch runtime.GOOS {
	case "windows":
		return "sh"
	default:
		return "/bin/sh"
	}
}
