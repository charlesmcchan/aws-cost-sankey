package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer/types"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Accounts  []Account `yaml:"accounts"`
	StartDate string    `yaml:"startDate"`
	EndDate   string    `yaml:"endDate"`
	Threshold float64   `yaml:"threshold"`
}

type Account struct {
	Name   string `yaml:"name"`
	Key    string `yaml:"key"`
	Secret string `yaml:"secret"`
	Token  string `yaml:"token"`
}

var results = make(map[string]map[string]float64)

func main() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)

	// Parse command line arguments
	configFile := flag.String("c", "configs/configs.yaml", "path to the config file")
	outputFile := flag.String("o", "output.txt", "path to the output file")
	flag.Parse()

	// Load config from file
	var config Config
	data, err := os.ReadFile(*configFile)
	if err != nil {
		log.Fatalf("error: %v", err)
	}
	err = yaml.Unmarshal(data, &config)
	if err != nil {
		log.Fatalf("error: %v", err)
	}

	for _, account := range config.Accounts {
		setEnvVar(account.Key, account.Secret, account.Token)
		fetchData(account.Name, config.StartDate, config.EndDate, config.Threshold)
	}
	outputResults(*outputFile)
}

func setEnvVar(key string, secret string, token string) {
	log.Printf("Setting environment variables\n")

	err := os.Setenv("AWS_ACCESS_KEY_ID", key)
	if err != nil {
		log.Fatalf("error setting AWS_ACCESS_KEY_ID: %v", err)
	}
	err = os.Setenv("AWS_SECRET_ACCESS_KEY", secret)
	if err != nil {
		log.Fatalf("error setting AWS_SECRET_ACCESS_KEY: %v", err)
	}
	err = os.Setenv("AWS_SESSION_TOKEN", token)
	if err != nil {
		log.Fatalf("error setting AWS_SESSION_TOKEN: %v", err)
	}
}

func fetchData(accountName string, startDate string, endDate string, threshold float64) {
	log.Printf("Fetching data for %s\n", accountName)

	cfg, err := config.LoadDefaultConfig(context.TODO(), config.WithRegion("us-west-2"))
	if err != nil {
		log.Fatalf("unable to load SDK config, %v", err)
	}

	svc := costexplorer.NewFromConfig(cfg)

	input := &costexplorer.GetCostAndUsageInput{
		TimePeriod: &types.DateInterval{
			Start: aws.String(startDate),
			End:   aws.String(endDate),
		},
		Granularity: types.GranularityMonthly,
		Metrics:     []string{"AmortizedCost"},
		GroupBy: []types.GroupDefinition{
			{Type: types.GroupDefinitionTypeTag, Key: aws.String("environment")},
			{Type: types.GroupDefinitionTypeDimension, Key: aws.String("SERVICE")},
		},
	}

	result, err := svc.GetCostAndUsage(context.TODO(), input)
	if err != nil {
		log.Fatalf("failed to get cost data: %v", err)
	}

	prepareResults(accountName, result, threshold)
}

func prepareResults(accountName string, result *costexplorer.GetCostAndUsageOutput, threshold float64) {
	for _, group := range result.ResultsByTime[0].Groups {
		service := group.Keys[0]
		environment := group.Keys[1]

		// Trim "environment$" prefix
		if len(environment) > len("environment$") {
			environment = environment[len("environment$"):]
		} else {
			environment = "unknown"
		}

		// Parse cost, ignore those below threshold
		amount := group.Metrics["AmortizedCost"].Amount
		amountFloat64, err := strconv.ParseFloat(*amount, 64)
		if err != nil {
			log.Fatalf("failed to parse amount: %v", err)
		}

		if amountFloat64 < threshold {
			continue
		}

		// Aggregate costs by account
		if _, ok := results["all"]; !ok {
			results["all"] = make(map[string]float64)
		}
		results["all"][accountName] += amountFloat64

		// Aggregate costs by environment
		if _, ok := results[accountName]; !ok {
			results[accountName] = make(map[string]float64)
		}
		results[accountName][environment] += amountFloat64

		// Aggregate costs by service within environment
		if _, ok := results[environment]; !ok {
			results[environment] = make(map[string]float64)
		}
		results[environment][service] += amountFloat64
	}
}

func outputResults(outputFile string) {
	f, err := os.OpenFile(outputFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Fatalf("failed to open output file: %v", err)
	}
	defer f.Close()

	for parent, children := range results {
		for child, cost := range children {
			result := fmt.Sprintf("%s [%.2f] %s\n", parent, cost, child)
			if _, err := f.WriteString(result); err != nil {
				log.Fatalf("failed to write to output file: %v", err)
			}
		}
	}
}
