package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"

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
	OpenAIKey string    `yaml:"openaiKey"`
	Model     string    `yaml:"model"`
	MaxTokens int       `yaml:"maxTokens"`
	Prompt    string    `yaml:"prompt"`
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
	configFile := flag.String("c", "configs/configs.yaml", "(Optional) Path to the config file")
	outputFile := flag.String("o", "output", "(Optional) Name of output file. Suffix will be determined by output format")
	format := flag.String("f", "chart", "(Optional) Output format: \"text\", \"chart\" or \"text+ai\" (plaintext with OpenAI analysis)")
	devMode := flag.Bool("d", false, "(Optional) Show UsageType instead of Service")
	inputFile := flag.String("i", "", "(Optional) Input text file from which the cost data will be read.\nIf not provided, data will be fetched from AWS Cost Explorer API")
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

	// Load results from file if inputFile is provided
	// Otherwise, fetch data from each account via AWS Cost Explorer API
	if *inputFile != "" {
		readData(*inputFile)
	} else {
		for _, account := range globalConfig.Accounts {
			setEnvVar(account.Name, account.Key, account.Secret, account.Token)
			fetchData(account.Name, *devMode)
		}
	}

	// Generate output to file or text
	var filename string
	if *format == "text" || *format == "text+ai" {
		filename = fmt.Sprintf("%s.txt", *outputFile)
		generateText(filename)
		if *format == "text+ai" {
			analyze(filename)
		}
	} else if *format == "chart" {
		filename = fmt.Sprintf("%s.html", *outputFile)
		generateChart(filename)
	} else {
		log.Fatalf("unknown format: %s", *format)
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

func readData(inputFile string) {
	log.Printf("Reading data from %s\n", inputFile)

	data, err := os.ReadFile(inputFile)
	if err != nil {
		log.Fatalf("error: %v", err)
	}

	lines := string(data)
	for _, line := range strings.Split(lines, "\n") {
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 3 {
			log.Fatalf("invalid line format: %s", line)
		}
		parent := parts[0]
		costStr := strings.Trim(parts[1], "[]")
		cost, err := strconv.ParseFloat(costStr, 64)
		if err != nil {
			log.Fatalf("failed to parse cost: %v", err)
		}
		child := strings.Join(parts[2:], " ")

		if _, ok := results[parent]; !ok {
			results[parent] = make(map[string]float64)
		}
		results[parent][child] = cost
	}
}

func fetchData(accountName string, devMode bool) {
	log.Printf("Fetching data for %s\n", accountName)

	// Region doesn't matter for cost explorer since its a global service
	cfg, err := config.LoadDefaultConfig(context.TODO(), config.WithRegion("us-east-1"))
	if err != nil {
		log.Fatalf("unable to load SDK config, %v", err)
	}

	svc := costexplorer.NewFromConfig(cfg)

	var groupBy []types.GroupDefinition
	if devMode {
		groupBy = []types.GroupDefinition{
			{Type: types.GroupDefinitionTypeTag, Key: aws.String("environment")},
			{Type: types.GroupDefinitionTypeDimension, Key: aws.String("USAGE_TYPE")},
		}
	} else {
		groupBy = []types.GroupDefinition{
			{Type: types.GroupDefinitionTypeTag, Key: aws.String("environment")},
			{Type: types.GroupDefinitionTypeDimension, Key: aws.String("SERVICE")},
		}
	}

	input := &costexplorer.GetCostAndUsageInput{
		TimePeriod: &types.DateInterval{
			Start: aws.String(globalConfig.StartDate),
			End:   aws.String(globalConfig.EndDate),
		},
		Granularity: types.GranularityMonthly,
		Metrics:     []string{"AmortizedCost"},
		GroupBy:     groupBy,
	}

	result, err := svc.GetCostAndUsage(context.TODO(), input)
	if err != nil {
		log.Fatalf("failed to get cost data: %v", err)
	}

	prepareResults(accountName, result)
}

func prepareResults(accountName string, result *costexplorer.GetCostAndUsageOutput) {
	for _, resultByTime := range result.ResultsByTime {
		log.Printf("Processing data for %s from %s to %s\n", accountName, *resultByTime.TimePeriod.Start, *resultByTime.TimePeriod.End)

		for _, group := range resultByTime.Groups {
			environment := group.Keys[0]
			service := group.Keys[1]

			// Prettify cluster name
			if len(environment) > len("environment$") {
				environment = environment[len("environment$"):]
			} else {
				environment = fmt.Sprintf("%s-unknown", accountName)
			}

			// Parse cost, round the fractions, and ignore those below threshold
			amount := group.Metrics["AmortizedCost"].Amount
			amountFloat64, err := strconv.ParseFloat(*amount, 32)
			amountFloat64 = math.Round(amountFloat64)
			if err != nil {
				log.Fatalf("failed to parse amount: %v", err)
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
	log.Printf("Generating text output...")

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
	log.Printf("Generating chart output...")

	sankeyNode := make([]opts.SankeyNode, 0)
	sankeyLink := make([]opts.SankeyLink, 0)

	// Add all links
	for parent, children := range results {
		for child, cost := range children {
			if cost >= globalConfig.Threshold {
				sankeyLink = append(sankeyLink, opts.SankeyLink{Source: parent, Target: child, Value: float32(cost)})
			}
		}
	}

	// Only add nodes that have links
	for _, link := range sankeyLink {
		var nodeName string
		nodeName = link.Source.(string)
		if !hasNode(nodeName, sankeyNode) {
			sankeyNode = append(sankeyNode, opts.SankeyNode{Name: nodeName})
		}
		nodeName = link.Target.(string)
		if !hasNode(nodeName, sankeyNode) {
			sankeyNode = append(sankeyNode, opts.SankeyNode{Name: nodeName})
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

	seriesName := fmt.Sprintf("%s-%s > $%.0f", globalConfig.StartDate, globalConfig.EndDate, globalConfig.Threshold)
	sankey.AddSeries(seriesName, sankeyNode, sankeyLink, charts.WithLabelOpts(opts.Label{
		Show:      opts.Bool(true),
		FontSize:  12,
		Formatter: "{c} {b}",
	}))

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

func analyze(filename string) {
	log.Printf("Analyzing with OpenAI...")

	data, err := os.ReadFile(filename)
	if err != nil {
		log.Fatalf("failed to read file: %v", err)
	}

	requestBody, err := json.Marshal(map[string]interface{}{
		"messages": []map[string]string{
			{"role": "system", "content": globalConfig.Prompt},
			{"role": "user", "content": string(data)},
		},
		"model":      globalConfig.Model,
		"max_tokens": globalConfig.MaxTokens,
	})

	if err != nil {
		log.Fatalf("failed to marshal request body: %v", err)
	}

	req, err := http.NewRequest("POST", "https://api.openai.com/v1/chat/completions", bytes.NewBuffer(requestBody))
	if err != nil {
		log.Fatalf("failed to create request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", globalConfig.OpenAIKey))

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		log.Fatalf("failed to send request: %v", err)
	}
	defer resp.Body.Close()

	var responseBody map[string]interface{}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Fatalf("failed to read response body: %v", err)
	}
	if err := json.Unmarshal(body, &responseBody); err != nil {
		log.Fatalf("failed to decode response body: %v", err)
	}

	choices, ok := responseBody["choices"].([]interface{})
	if !ok || len(choices) == 0 {
		log.Fatalf("no choices in response body")
	}

	message, ok := choices[0].(map[string]interface{})["message"].(map[string]interface{})
	if !ok {
		log.Fatalf("no message in first choice")
	}
	text, ok := message["content"].(string)
	if !ok {
		log.Fatalf("no content in message")
	}

	log.Printf("OpenAI analysis:\n%s", text)
}
