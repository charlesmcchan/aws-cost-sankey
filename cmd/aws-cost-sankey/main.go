package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer/types"
	"github.com/go-echarts/go-echarts/v2/charts"
	"github.com/go-echarts/go-echarts/v2/components"
	"github.com/go-echarts/go-echarts/v2/opts"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Accounts  []Account `yaml:"accounts"`
	StartDate string    `yaml:"startDate"`
	EndDate   string    `yaml:"endDate"`
	Threshold float64   `yaml:"threshold"`
	Height    string    `yaml:"height"`
	Width     string    `yaml:"width"`
}

type Account struct {
	Name   string `yaml:"name"`
	Key    string `yaml:"key"`
	Secret string `yaml:"secret"`
	Token  string `yaml:"token"`
}

var globalConfig Config
var results = make(map[string]map[string]float64)

func main() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)

	// Parse command line arguments
	configFile := flag.String("c", "configs/configs.yaml", "path to the config file")
	outputFile := flag.String("o", "output", "name of output file")
	mode := flag.String("m", "chart", "output mode: text or chart")
	flag.Parse()

	// Load config from file
	data, err := os.ReadFile(*configFile)
	if err != nil {
		log.Fatalf("error: %v", err)
	}
	err = yaml.Unmarshal(data, &globalConfig)
	if err != nil {
		log.Fatalf("error: %v", err)
	}

	for _, account := range globalConfig.Accounts {
		setEnvVar(account.Name, account.Key, account.Secret, account.Token)
		fetchData(account.Name)
	}

	// Generate output to file or text
	var filename string
	if *mode == "text" {
		filename = fmt.Sprintf("%s.txt", *outputFile)
		generateText(filename)
	}
	if *mode == "chart" {
		filename = fmt.Sprintf("%s.html", *outputFile)
		generateChart(filename)
	}
}

func setEnvVar(name string, key string, secret string, token string) {
	log.Printf("Setting environment variables for %s\n", name)

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

func fetchData(accountName string) {
	log.Printf("Fetching data for %s\n", accountName)

	// Region doesn't matter for cost explorer since its a global service
	cfg, err := config.LoadDefaultConfig(context.TODO(), config.WithRegion("us-east-1"))
	if err != nil {
		log.Fatalf("unable to load SDK config, %v", err)
	}

	svc := costexplorer.NewFromConfig(cfg)

	input := &costexplorer.GetCostAndUsageInput{
		TimePeriod: &types.DateInterval{
			Start: aws.String(globalConfig.StartDate),
			End:   aws.String(globalConfig.EndDate),
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

	prepareResults(accountName, result, globalConfig.Threshold)
}

func prepareResults(accountName string, result *costexplorer.GetCostAndUsageOutput, threshold float64) {
	for _, resultByTime := range result.ResultsByTime {
		log.Printf("Processing data for %s from %s to %s\n", accountName, *resultByTime.TimePeriod.Start, *resultByTime.TimePeriod.End)

		results = make(map[string]map[string]float64)

		for _, group := range resultByTime.Groups {
			environment := group.Keys[0]
			service := group.Keys[1]

			// Prettify cluster name
			if len(environment) > len("environment$") {
				environment = environment[len("environment$"):]
			} else {
				environment = fmt.Sprintf("%s-unknown", accountName)
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
}

func generateText(outputFile string) {
	log.Printf("Generating text...")

	f, err := os.Create(outputFile)
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

func generateChart(outputFile string) {
	log.Printf("Generating charts...")

	sankeyNode := make([]opts.SankeyNode, 0)
	sankeyLink := make([]opts.SankeyLink, 0)

	for parent, children := range results {
		if !hasNode(parent, sankeyNode) {
			sankeyNode = append(sankeyNode, opts.SankeyNode{Name: parent})
		}
		for child, cost := range children {
			if !hasNode(child, sankeyNode) {
				sankeyNode = append(sankeyNode, opts.SankeyNode{Name: child})
			}
			sankeyLink = append(sankeyLink, opts.SankeyLink{Source: parent, Target: child, Value: float32(cost)})
		}
	}

	sankey := charts.NewSankey()
	sankey.SetGlobalOptions(
		charts.WithTitleOpts(opts.Title{
			Title: "AWS Cost Analysis",
		}),
		charts.WithInitializationOpts(opts.Initialization{
			Width:  globalConfig.Width,
			Height: globalConfig.Height,
			Theme:  "westeros",
		}),
	)

	seriesName := fmt.Sprintf("%s-%s", globalConfig.StartDate, globalConfig.EndDate)
	sankey.AddSeries(seriesName, sankeyNode, sankeyLink, charts.WithLabelOpts(opts.Label{Show: opts.Bool(true)}))

	page := components.NewPage()
	page.AddCharts(sankey)

	f, err := os.Create(outputFile)
	if err != nil {
		log.Fatalf("error: %v", err)
	}
	page.Render(io.MultiWriter(f))
}

func hasNode(name string, nodes []opts.SankeyNode) bool {
	for _, n := range nodes {
		if n.Name == name {
			return true
		}
	}
	return false
}
