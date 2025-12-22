package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// Configuration constants
const (
	mongoURI          = "mongodb://127.0.0.1:17017"
	eloqdocURI        = "mongodb://127.0.0.1:27017"
	dbName            = "benchmark_db_11"
	singleInsertCount = 100
	batchSize         = 100
	numBatches        = 100 // Number of batches to test in batch insert
	maxRetries        = 4
	retryDelayMs      = 10000
	loaderThreads     = 3 // Number of worker threads for document loading
	benchmarkDataDir  = "./benchmark_data"
	deleteBatchSize   = 500
)

// BenchmarkConfig pairs a data volume with its specific document sizes
type BenchmarkConfig struct {
	DataVolume    int
	DocumentSizes []int
}

// Configuration variables
var (
	warmupQueryCount = 0
	queryIterations  = 100000

	// Define data volumes with their specific document sizes
	benchmarkConfigs = []BenchmarkConfig{
		// {DataVolume: 10000, DocumentSizes: []int{429497, 858993, 1503239, 1932735}},
		{DataVolume: 10000, DocumentSizes: []int{858993}},
	}
)

/*
	benchmarkConfigs = []BenchmarkConfig{
		{DataVolume: 10000, DocumentSizes: []int{429497, 858993, 1503239, 1932735}},
		{DataVolume: 50000, DocumentSizes: []int{85899, 171799, 300648, 386547}},
		{DataVolume: 100000, DocumentSizes: []int{42950, 85899, 150324, 193274}},
		{DataVolume: 500000, DocumentSizes: []int{8590, 17180, 30065, 38655}},
	}
*/

// Calculated batch insert count
var batchInsertCount = batchSize * numBatches

// Cart represents the document structure
type Cart struct {
	ID                    string                 `bson:"_id" json:"_id"`
	Token                 string                 `bson:"token" json:"token"`
	Note                  string                 `bson:"note" json:"note"`
	Attributes            map[string]interface{} `bson:"attributes" json:"attributes"`
	OriginalTotalPrice    int                    `bson:"original_total_price" json:"original_total_price"`
	TotalPrice            int                    `bson:"total_price" json:"total_price"`
	TotalDiscount         int                    `bson:"total_discount" json:"total_discount"`
	TotalWeight           int                    `bson:"total_weight" json:"total_weight"`
	ItemCount             int                    `bson:"item_count" json:"item_count"`
	Items                 []Item                 `bson:"items" json:"items"`
	RequiresShipping      bool                   `bson:"requires_shipping" json:"requires_shipping"`
	Currency              string                 `bson:"currency" json:"currency"`
	ItemsSubtotalPrice    int                    `bson:"items_subtotal_price" json:"items_subtotal_price"`
	CartLevelDiscountApps []interface{}          `bson:"cart_level_discount_applications" json:"cart_level_discount_applications"`
	DiscountCodes         []interface{}          `bson:"discount_codes" json:"discount_codes"`
	PaddingData           string                 `bson:"padding_data" json:"padding_data"`
}

// Item represents an item in the cart
type Item struct {
	ID                           int64                  `bson:"id" json:"id"`
	Properties                   map[string]interface{} `bson:"properties" json:"properties"`
	Quantity                     int                    `bson:"quantity" json:"quantity"`
	VariantID                    int64                  `bson:"variant_id" json:"variant_id"`
	Key                          string                 `bson:"key" json:"key"`
	Title                        string                 `bson:"title" json:"title"`
	Price                        int                    `bson:"price" json:"price"`
	OriginalPrice                int                    `bson:"original_price" json:"original_price"`
	PresentmentPrice             int                    `bson:"presentment_price" json:"presentment_price"`
	DiscountedPrice              int                    `bson:"discounted_price" json:"discounted_price"`
	LinePrice                    int                    `bson:"line_price" json:"line_price"`
	OriginalLinePrice            int                    `bson:"original_line_price" json:"original_line_price"`
	TotalDiscount                int                    `bson:"total_discount" json:"total_discount"`
	Discounts                    []interface{}          `bson:"discounts" json:"discounts"`
	SKU                          string                 `bson:"sku" json:"sku"`
	Grams                        int                    `bson:"grams" json:"grams"`
	Vendor                       string                 `bson:"vendor" json:"vendor"`
	Taxable                      bool                   `bson:"taxable" json:"taxable"`
	ProductID                    int64                  `bson:"product_id" json:"product_id"`
	ProductHasOnlyDefaultVariant bool                   `bson:"product_has_only_default_variant" json:"product_has_only_default_variant"`
	GiftCard                     bool                   `bson:"gift_card" json:"gift_card"`
	FinalPrice                   int                    `bson:"final_price" json:"final_price"`
	FinalLinePrice               int                    `bson:"final_line_price" json:"final_line_price"`
	URL                          string                 `bson:"url" json:"url"`
	FeaturedImage                map[string]interface{} `bson:"featured_image" json:"featured_image"`
	Image                        string                 `bson:"image" json:"image"`
	Handle                       string                 `bson:"handle" json:"handle"`
	RequiresShipping             bool                   `bson:"requires_shipping" json:"requires_shipping"`
	ProductType                  string                 `bson:"product_type" json:"product_type"`
	ProductTitle                 string                 `bson:"product_title" json:"product_title"`
	VariantTitle                 *string                `bson:"variant_title" json:"variant_title"`
	VariantOptions               []string               `bson:"variant_options" json:"variant_options"`
	OptionsWithValues            []map[string]string    `bson:"options_with_values" json:"options_with_values"`
	LineLevelDiscountAllocations []interface{}          `bson:"line_level_discount_allocations" json:"line_level_discount_allocations"`
	LineLevelTotalDiscount       int                    `bson:"line_level_total_discount" json:"line_level_total_discount"`
	QuantityRule                 map[string]interface{} `bson:"quantity_rule" json:"quantity_rule"`
	HasComponents                bool                   `bson:"has_components" json:"has_components"`
}

// QueryParams holds pre-generated query parameters
type QueryParams struct {
	ExactMatchID     int64
	RangeMinPrice    int
	RangeMaxPrice    int
	AndQuantity      int
	AndMinGrams      int
	OrVariantID1     int64
	OrVariantID2     int64
	ArrayContainsID  int64
	NestedPrice      int
	ComplexMinPrice  int
	ComplexMaxGrams  int
	ComplexVariantID int64
}

// BenchmarkResult holds the results of a benchmark
type BenchmarkResult struct {
	Operation  string  `json:"operation"`
	DataVolume int     `json:"data_volume"`
	DocSize    int     `json:"doc_size_bytes"`
	Database   string  `json:"database"`
	P50        float64 `json:"p50_ms"`
	P80        float64 `json:"p80_ms"`
	P90        float64 `json:"p90_ms"`
	P95        float64 `json:"p95_ms"`
	P99        float64 `json:"p99_ms"`
	Mean       float64 `json:"mean_ms"`
	StdDev     float64 `json:"std_dev_ms"`
	Min        float64 `json:"min_ms"`
	Max        float64 `json:"max_ms"`
	SampleSize int     `json:"sample_size"`
	OpsPerSec  float64 `json:"ops_per_sec"`
}

var results []BenchmarkResult

type ProgressReporter struct {
	total   int64
	current int64
	prefix  string
	mu      sync.Mutex
}

func NewProgressReporter(total int, prefix string) *ProgressReporter {
	return &ProgressReporter{
		total:  int64(total),
		prefix: prefix,
	}
}

func (p *ProgressReporter) Increment(n int) {
	current := atomic.AddInt64(&p.current, int64(n))
	total := atomic.LoadInt64(&p.total)

	p.mu.Lock()
	fmt.Printf("\r  %s: %d/%d (%.1f%%)", p.prefix, current, total,
		float64(current)/float64(total)*100)
	if current >= total {
		fmt.Println() // New line when complete
	}
	p.mu.Unlock()
}

func (p *ProgressReporter) Complete() {
	p.mu.Lock()
	fmt.Printf("\r  %s: %d/%d (100.0%%)\n", p.prefix, p.total, p.total)
	p.mu.Unlock()
}

func main() {
	// Parse command-line flags
	mongoOnly := flag.Bool("mongo-only", false, "Run only MongoDB benchmarks")
	eloqdocOnly := flag.Bool("eloqdoc-only", false, "Run only EloqDoc benchmarks")

	outputFormat := flag.String("output", "table", "Output format: table, json, csv")
	outputFile := flag.String("output-file", "", "Output file path (optional, defaults to stdout for table, or auto-generated filename)")
	flag.Parse()

	startTime := time.Now()

	// Validate configuration
	minRequiredDocs := singleInsertCount
	if batchInsertCount > minRequiredDocs {
		minRequiredDocs = batchInsertCount
	}

	for _, config := range benchmarkConfigs {
		if config.DataVolume < minRequiredDocs {
			log.Fatalf("Error: Data volume %d is less than required minimum %d (max of singleInsertCount=%d and batchInsertCount=%d)",
				config.DataVolume, minRequiredDocs, singleInsertCount, batchInsertCount)
		}
	}

	// Display configuration
	fmt.Println("=== Database Benchmarking Tool ===")
	fmt.Printf("MongoDB URI: %s\n", mongoURI)
	fmt.Printf("EloqDoc URI: %s\n", eloqdocURI)
	fmt.Println("Benchmark Configurations:")
	for i, config := range benchmarkConfigs {
		fmt.Printf("  %d. Volume: %d documents, Sizes: %v bytes\n", i+1, config.DataVolume, config.DocumentSizes)
	}
	fmt.Printf("Single Insert Count: %d\n", singleInsertCount)
	fmt.Printf("Batch Size: %d\n", batchSize)
	fmt.Printf("Number of Batches: %d\n", numBatches)
	fmt.Printf("Batch Insert Count: %d\n", batchInsertCount)
	fmt.Printf("Query Iterations: %d\n", queryIterations)
	fmt.Printf("Warmup Queries: %d\n", warmupQueryCount)
	fmt.Printf("Loader Threads: %d\n", loaderThreads)
	fmt.Printf("Output Format: %s\n", *outputFormat)

	if *mongoOnly {
		fmt.Println("Mode: MongoDB Only")
	} else if *eloqdocOnly {
		fmt.Println("Mode: EloqDoc Only")
	} else {
		fmt.Println("Mode: Both Databases")
	}
	fmt.Println()

	// Benchmark each configuration (volume + its specific doc sizes)
	for _, config := range benchmarkConfigs {
		volume := config.DataVolume

		for _, docSize := range config.DocumentSizes {
			fmt.Printf("\n=== Testing with %d documents of size ~%d bytes ===\n", volume, docSize)

			// Track collections and folders for this iteration only
			var iterationCollections []collectionInfo
			var iterationFolders []string

			fmt.Println("Generating documents to disk...")

			var mongoFolder, eloqdocFolder string
			var err error

			if !*eloqdocOnly {
				mongoFolder, err = generateDocumentsToFolder(volume, docSize, "MongoDB")
				if err != nil {
					log.Fatalf("Failed to generate MongoDB documents: %v", err)
				}
				iterationFolders = append(iterationFolders, mongoFolder)
				fmt.Printf("  ✓ Generated %d MongoDB documents to %s\n", volume, mongoFolder)
			}

			if !*mongoOnly {
				eloqdocFolder, err = generateDocumentsToFolder(volume, docSize, "EloqDoc")
				if err != nil {
					log.Fatalf("Failed to generate EloqDoc documents: %v", err)
				}
				iterationFolders = append(iterationFolders, eloqdocFolder)
				fmt.Printf("  ✓ Generated %d EloqDoc documents to %s\n", volume, eloqdocFolder)
			}

			fmt.Println("Pre-generating query parameters...")
			var queryParams []QueryParams

			ctx, cancel := context.WithCancel(context.Background())

			if !*eloqdocOnly {
				sampleDocs := collectDocumentsFromChannel(
					ctx,
					loadDocumentsInBatches(ctx, mongoFolder, 100, loaderThreads),
					queryIterations,
				)
				queryParams = generateQueryParams(sampleDocs, queryIterations)
			} else if !*mongoOnly {
				sampleDocs := collectDocumentsFromChannel(
					ctx,
					loadDocumentsInBatches(ctx, eloqdocFolder, 100, loaderThreads),
					queryIterations,
				)
				queryParams = generateQueryParams(sampleDocs, queryIterations)
			}

			cancel() // Clean up context

			// Benchmark MongoDB
			if !*eloqdocOnly {
				fmt.Println("\nBenchmarking MongoDB...")
				mongoCollections := benchmarkDatabase("MongoDB", mongoURI, volume, docSize, mongoFolder, queryParams)
				iterationCollections = append(iterationCollections, mongoCollections...)
			}

			// Benchmark EloqDoc
			if !*mongoOnly {
				fmt.Println("\nBenchmarking EloqDoc...")
				eloqdocCollections := benchmarkDatabase("EloqDoc", eloqdocURI, volume, docSize, eloqdocFolder, queryParams)
				iterationCollections = append(iterationCollections, eloqdocCollections...)
			}

			// Clean up after THIS iteration to free up space
			fmt.Printf("\n=== Cleaning up iteration (Volume: %d, DocSize: %d) ===\n", volume, docSize)

			// Drop collections from this iteration
			fmt.Println("  Dropping collections...")
			cleanupCollections(iterationCollections)

			// Remove document folders from this iteration
			fmt.Println("  Removing document folders...")
			cleanupFolders(iterationFolders)

			fmt.Printf("  ✓ Cleanup complete - freed disk and database space\n")
		}
	}

	// FIX #10: Display/export results based on output format
	fmt.Println("\n\n=== Benchmark Results ===\n")

	switch *outputFormat {
	case "json":
		if err := exportResultsJSON(*outputFile); err != nil {
			log.Printf("Error exporting JSON: %v", err)
		}
	case "csv":
		if err := exportResultsCSV(*outputFile); err != nil {
			log.Printf("Error exporting CSV: %v", err)
		}
	default:
		displayResults()
	}

	// Display total execution time
	duration := time.Since(startTime)
	fmt.Printf("\nTotal Execution Time: %s\n", duration)
}

type collectionInfo struct {
	uri        string
	dbName     string
	collection string
}

type insertStats struct {
	successful int
	failed     int
	retries    int
}

func retryOperation(operation func() error, maxRetries int) error {
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			delay := time.Duration(retryDelayMs*(1<<uint(attempt-1))) * time.Millisecond
			time.Sleep(delay)
			log.Printf("    Retry attempt %d/%d after %v delay", attempt, maxRetries, delay)
		}

		err := operation()
		if err == nil {
			return nil
		}
		lastErr = err
	}
	return fmt.Errorf("operation failed after %d retries: %v", maxRetries, lastErr)
}

// deleteBatched deletes documents in batches to avoid memory limits
func deleteBatched(ctx context.Context, coll *mongo.Collection, ids []string) (int64, error) {
	var totalDeleted int64

	for i := 0; i < len(ids); i += deleteBatchSize {
		end := i + deleteBatchSize
		if end > len(ids) {
			end = len(ids)
		}

		batchIDs := ids[i:end]
		deleteFilter := bson.M{"_id": bson.M{"$in": batchIDs}}

		var deleteResult *mongo.DeleteResult
		err := retryOperation(func() error {
			var err error
			deleteResult, err = coll.DeleteMany(ctx, deleteFilter)
			return err
		}, maxRetries)

		if err != nil {
			return totalDeleted, fmt.Errorf("failed to delete batch %d-%d: %w", i, end, err)
		}

		totalDeleted += deleteResult.DeletedCount
	}

	return totalDeleted, nil
}

// generatePaddingString creates a string of approximately the specified size
func generatePaddingString(size int) string {
	if size <= 0 {
		return ""
	}
	pattern := "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789abcdefghijklmnopqrstuvwxyz"
	repetitions := (size / len(pattern)) + 1
	padding := strings.Repeat(pattern, repetitions)
	return padding[:size]
}

// generateDocumentsToFolder generates documents one at a time and saves to disk
func generateDocumentsToFolder(count int, targetSize int, dbType string) (string, error) {
	// Create folder structure
	folderPath := filepath.Join(benchmarkDataDir, fmt.Sprintf("%s_%d_%d", dbType, count, targetSize))
	if err := os.MkdirAll(folderPath, 0755); err != nil {
		return "", fmt.Errorf("failed to create folder: %w", err)
	}

	// Calculate base document size
	baseDoc := Cart{
		ID:    "sample_id_with_uuid",
		Token: "hWN4vswT9UEwPM1iWZDVx6FO?key=f6de207245be7cd84be6a4738fc3d749",
		Items: []Item{
			{
				ID:        40000000000000,
				VariantID: 40000000000000,
				Key:       "40426232021193:12345678901234567890123456789012",
				Title:     "Ninja Luxe Café Espresso Machine (Stainless Steel)",
				SKU:       "800000",
				Vendor:    "NINJA",
			},
		},
	}

	baseJSON, err := json.Marshal(baseDoc)
	if err != nil {
		return "", fmt.Errorf("failed to marshal base document: %w", err)
	}
	baseSize := len(baseJSON)

	// Calculate padding needed
	paddingSize := targetSize - baseSize
	if paddingSize < 0 {
		paddingSize = 0
		log.Printf("Warning: Base document size (%d bytes) exceeds target size (%d bytes)", baseSize, targetSize)
	}

	paddingString := generatePaddingString(paddingSize)

	progress := NewProgressReporter(count, "Generating documents")

	// Generate and save documents one at a time
	for i := 0; i < count; i++ {
		doc := Cart{
			ID:                 fmt.Sprintf("%d_%s", i, uuid.New().String()),
			Token:              "hWN4vswT9UEwPM1iWZDVx6FO?key=f6de207245be7cd84be6a4738fc3d749",
			Note:               "",
			Attributes:         make(map[string]interface{}),
			OriginalTotalPrice: 69800,
			TotalPrice:         69800,
			TotalDiscount:      0,
			TotalWeight:        15500,
			ItemCount:          1,
			Items: []Item{
				{
					ID: 40000000000000 + rand.Int63n(99999999999),
					Properties: map[string]interface{}{
						"_availabilityCanPreOrder": "false",
						"_availabilityFulfilment":  "Physical",
						"_maxQty":                  "5",
						"_priceAtc":                "69800",
						"_itemListName":            "Homepage - ",
						"_productGroup":            "528",
						"_eligibleForServices":     "true",
						"_shouldSplit":             uuid.New().String(),
					},
					Quantity:                     rand.Intn(10) + 1,
					VariantID:                    40000000000000 + rand.Int63n(99999999999),
					Key:                          fmt.Sprintf("%d:%s", 40426232021193, uuid.New().String()[:32]),
					Title:                        "Ninja Luxe Café Espresso Machine (Stainless Steel)",
					Price:                        rand.Intn(100000) + 10000,
					OriginalPrice:                rand.Intn(100000) + 10000,
					PresentmentPrice:             698,
					DiscountedPrice:              69800,
					LinePrice:                    69800,
					OriginalLinePrice:            69800,
					TotalDiscount:                0,
					Discounts:                    []interface{}{},
					SKU:                          fmt.Sprintf("%d", 800000+rand.Intn(100000)),
					Grams:                        rand.Intn(20000) + 1000,
					Vendor:                       "NINJA",
					Taxable:                      true,
					ProductID:                    7000000000000 + rand.Int63n(999999999),
					ProductHasOnlyDefaultVariant: true,
					GiftCard:                     false,
					FinalPrice:                   69800,
					FinalLinePrice:               69800,
					URL:                          "/products/ninja-luxe-cafe-espresso-machine-stainless-steel?variant=40426232021193",
					FeaturedImage: map[string]interface{}{
						"aspect_ratio": 1,
						"alt":          "Ninja Luxe Café Espresso Machine (Stainless Steel)",
						"height":       2048,
						"width":        2048,
					},
					Image:            "",
					Handle:           "ninja-luxe-cafe-espresso-machine-stainless-steel",
					RequiresShipping: true,
					ProductType:      "SMALL APPLIANCES",
					ProductTitle:     "Ninja Luxe Café Espresso Machine (Stainless Steel)",
					VariantTitle:     nil,
					VariantOptions:   []string{"Default Title"},
					OptionsWithValues: []map[string]string{
						{"name": "Title", "value": "Default Title"},
					},
					LineLevelDiscountAllocations: []interface{}{},
					LineLevelTotalDiscount:       0,
					QuantityRule: map[string]interface{}{
						"min":       1,
						"max":       nil,
						"increment": 1,
					},
					HasComponents: false,
				},
			},
			RequiresShipping:      true,
			Currency:              "AUD",
			ItemsSubtotalPrice:    69800,
			CartLevelDiscountApps: []interface{}{},
			DiscountCodes:         []interface{}{},
			PaddingData:           paddingString,
		}

		// Save document to file
		filename := filepath.Join(folderPath, fmt.Sprintf("doc_%d.json", i))

		data, err := json.Marshal(doc)
		if err != nil {
			return "", fmt.Errorf("failed to marshal document %d: %w", i, err)
		}

		if err := os.WriteFile(filename, data, 0644); err != nil {
			return "", fmt.Errorf("failed to write document %d: %w", i, err)
		}

		// Log progress periodically
		if i == 0 {
			actualSize := len(data)
			fmt.Printf("\n  Target size: %d bytes, Actual size: %d bytes (diff: %+d bytes)\n",
				targetSize, actualSize, actualSize-targetSize)
		}

		if (i+1)%1000 == 0 {
			progress.Increment(1000)
		}
	}

	// Complete progress
	progress.Complete()

	return folderPath, nil
}

func loadDocumentsInBatches(ctx context.Context, folderPath string, batchSize int, numWorkers int) <-chan []Cart {
	resultChan := make(chan []Cart, numWorkers*2) // Buffered channel

	go func() {
		defer close(resultChan)

		files, err := os.ReadDir(folderPath)
		if err != nil {
			log.Printf("Error reading folder %s: %v", folderPath, err)
			return
		}

		// Filter for JSON files
		var docFiles []string
		for _, file := range files {
			if !file.IsDir() && filepath.Ext(file.Name()) == ".json" {
				docFiles = append(docFiles, filepath.Join(folderPath, file.Name()))
			}
		}

		// Create work queue
		workQueue := make(chan []string, len(docFiles)/batchSize+1)
		var wg sync.WaitGroup

		// Start workers
		for w := 0; w < numWorkers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					select {
					case <-ctx.Done():
						return
					case fileBatch, ok := <-workQueue:
						if !ok {
							return
						}
						batch := make([]Cart, 0, len(fileBatch))

						for _, filename := range fileBatch {
							// Check context before each file read
							select {
							case <-ctx.Done():
								return
							default:
							}

							data, err := os.ReadFile(filename)
							if err != nil {
								log.Printf("Error reading file %s: %v", filename, err)
								continue
							}

							var doc Cart
							if err := json.Unmarshal(data, &doc); err != nil {
								log.Printf("Error unmarshaling file %s: %v", filename, err)
								continue
							}

							batch = append(batch, doc)
						}

						if len(batch) > 0 {
							select {
							case <-ctx.Done():
								return
							case resultChan <- batch:
							}
						}
					}
				}
			}()
		}

		// Distribute work
		for i := 0; i < len(docFiles); i += batchSize {
			end := i + batchSize
			if end > len(docFiles) {
				end = len(docFiles)
			}
			select {
			case <-ctx.Done():
				close(workQueue)
				wg.Wait()
				return
			case workQueue <- docFiles[i:end]:
			}
		}

		close(workQueue)
		wg.Wait()
	}()

	return resultChan
}

func collectDocumentsFromChannel(ctx context.Context, docChan <-chan []Cart, maxDocs int) []Cart {
	var docs []Cart
	for {
		select {
		case <-ctx.Done():
			return docs
		case batch, ok := <-docChan:
			if !ok {
				// Channel closed, return what we have
				if len(docs) > maxDocs {
					return docs[:maxDocs]
				}
				return docs
			}
			docs = append(docs, batch...)
			if len(docs) >= maxDocs {
				return docs[:maxDocs]
			}
		}
	}
}

func generateQueryParams(docs []Cart, count int) []QueryParams {
	params := make([]QueryParams, count)

	for i := 0; i < count; i++ {
		// Select random documents for query parameters
		doc1 := docs[rand.Intn(len(docs))]
		doc2 := docs[rand.Intn(len(docs))]
		doc3 := docs[rand.Intn(len(docs))]

		item1 := doc1.Items[0]
		item2 := doc2.Items[0]
		item3 := doc3.Items[0]

		params[i] = QueryParams{
			ExactMatchID:     item1.ID,
			RangeMinPrice:    item1.Price - 5000,
			RangeMaxPrice:    item1.Price + 5000,
			AndQuantity:      item1.Quantity,
			AndMinGrams:      item1.Grams - 1000,
			OrVariantID1:     item1.VariantID,
			OrVariantID2:     item2.VariantID,
			ArrayContainsID:  item1.ID,
			NestedPrice:      item1.Price,
			ComplexMinPrice:  item3.Price - 3000,
			ComplexMaxGrams:  item3.Grams + 1000,
			ComplexVariantID: item3.VariantID,
		}
	}

	return params
}

func benchmarkDatabase(dbType, uri string, volume int, docSize int, folderPath string, queryParams []QueryParams) []collectionInfo {
	ctx := context.Background()

	// Configure client with better connection settings
	clientOpts := options.Client().
		ApplyURI(uri).
		SetMaxPoolSize(100).
		SetMinPoolSize(10).
		SetMaxConnecting(10).
		SetConnectTimeout(20 * time.Second).
		SetServerSelectionTimeout(10 * time.Second).
		SetSocketTimeout(40 * time.Second)

	client, err := mongo.Connect(ctx, clientOpts)
	if err != nil {
		log.Fatalf("Failed to connect to %s: %v", dbType, err)
	}
	defer client.Disconnect(ctx)

	// Ping to verify connection
	if err := client.Ping(ctx, nil); err != nil {
		log.Fatalf("Failed to ping %s: %v", dbType, err)
	}

	// Use single collection for all tests
	collName := fmt.Sprintf("benchmark_%s_%d_%d", dbType, volume, docSize)
	coll := client.Database(dbName).Collection(collName)

	// Drop and recreate collection with all documents
	fmt.Printf("\n  Setting up collection with %d documents...\n", volume)
	err = retryOperation(func() error {
		return coll.Drop(ctx)
	}, maxRetries)
	if err != nil {
		log.Printf("    Warning: Could not drop collection after %d retries: %v", maxRetries, err)
	}

	// Insert all documents initially - load in batches from disk
	fmt.Println("  Loading and inserting documents from disk...")

	loadCtx, loadCancel := context.WithCancel(ctx)
	docChan := loadDocumentsInBatches(loadCtx, folderPath, batchSize, loaderThreads)
	insertedCount := 0

	progress := NewProgressReporter(volume, "Inserting documents")

	for batch := range docChan {
		batchInterface := make([]interface{}, len(batch))
		for i, doc := range batch {
			batchInterface[i] = doc
		}

		err = retryOperation(func() error {
			_, err := coll.InsertMany(ctx, batchInterface)
			if mongo.IsDuplicateKeyError(err) {
				err = nil
			}
			return err
		}, maxRetries)

		if err != nil {
			loadCancel()
			log.Fatalf("Failed to insert batch: %v", err)
		}

		insertedCount += len(batch)
		progress.Increment(len(batch))
	}
	loadCancel()

	// Verify initial document count
	var count int64
	err = retryOperation(func() error {
		var err error
		count, err = coll.CountDocuments(ctx, bson.M{})
		return err
	}, maxRetries)
	if err != nil {
		log.Printf("    Warning: Could not verify document count after %d retries: %v", maxRetries, err)
	} else {
		fmt.Printf("   Verified %d documents in collection\n", count)
	}

	// Load documents needed for insert benchmarks
	fmt.Println("\n  Loading documents for insert benchmarks...")

	singleCtx, singleCancel := context.WithCancel(ctx)
	singleInsertDocs := collectDocumentsFromChannel(
		singleCtx,
		loadDocumentsInBatches(singleCtx, folderPath, 100, loaderThreads),
		singleInsertCount,
	)
	singleCancel()
	fmt.Printf("  ✓ Loaded %d documents for single insert benchmark\n", len(singleInsertDocs))

	batchCtx, batchCancel := context.WithCancel(ctx)
	batchInsertDocs := collectDocumentsFromChannel(
		batchCtx,
		loadDocumentsInBatches(batchCtx, folderPath, batchSize, loaderThreads),
		batchInsertCount,
	)
	batchCancel()
	fmt.Printf("  ✓ Loaded %d documents for batch insert benchmark\n", len(batchInsertDocs))

	// Single Insert Benchmark
	benchmarkSingleInsert(ctx, coll, dbType, singleInsertDocs, volume, docSize)

	// Batch Insert Benchmark
	benchmarkBatchInsert(ctx, coll, dbType, batchInsertDocs, volume, docSize)

	// Query Benchmarks
	benchmarkQueries(ctx, coll, dbType, queryParams, volume, docSize)

	return []collectionInfo{{uri, dbName, collName}}
}

func benchmarkSingleInsert(ctx context.Context, coll *mongo.Collection, dbType string, docs []Cart, volume int, docSize int) {
	fmt.Printf("\n  === Single Insert Benchmark ===\n")

	// Get IDs of documents to delete
	idsToDelete := make([]string, len(docs))
	for i, doc := range docs {
		idsToDelete[i] = doc.ID
	}

	// Delete documents using batched delete to avoid memory limits
	fmt.Printf("  Deleting %d documents (in batches of %d)...\n", len(idsToDelete), deleteBatchSize)
	deletedCount, err := deleteBatched(ctx, coll, idsToDelete)
	if err != nil {
		log.Fatalf("Failed to delete documents for single insert test: %v", err)
	}
	fmt.Printf("  ✓ Deleted %d documents\n", deletedCount)

	// Verify deletion completed with retry logic
	deleteFilter := bson.M{"_id": bson.M{"$in": idsToDelete}}
	var count int64
	err = retryOperation(func() error {
		var err error
		count, err = coll.CountDocuments(ctx, deleteFilter)
		return err
	}, maxRetries)
	if err != nil {
		log.Printf("   Warning: Could not verify deletion after %d retries: %v", maxRetries, err)
	} else if count > 0 {
		log.Printf("   Warning: %d documents still exist after deletion\n", count)
	} else {
		fmt.Printf("  ✓ Verified deletion completed\n")
	}

	// Run single insert benchmark
	durations := make([]time.Duration, 0, len(docs))
	stats := insertStats{}

	progress := NewProgressReporter(len(docs), "Single insert")

	for i, doc := range docs {
		var duration time.Duration

		err := retryOperation(func() error {
			start := time.Now()
			_, err := coll.InsertOne(ctx, doc)
			if err == nil {
				duration = time.Since(start)
			}
			return err
		}, maxRetries)

		if err != nil {
			log.Printf("  Error inserting document %d after retries: %v", i, err)
			stats.failed++
		} else {
			durations = append(durations, duration)
			stats.successful++
		}

		if (i+1)%10 == 0 {
			progress.Increment(10)
		}
	}
	progress.Complete()

	if stats.failed > 0 {
		log.Printf("   Single Insert Stats: %d successful, %d failed (%.1f%% success rate)",
			stats.successful, stats.failed, float64(stats.successful)/float64(len(docs))*100)
	}

	if len(durations) == 0 {
		log.Printf("  No successful inserts - skipping metrics")
		return
	}

	result := calculateMetrics("Single Insert", volume, docSize, dbType, durations)
	results = append(results, result)

	fmt.Printf("  ✓ Single Insert: p50=%.2fms, p99=%.2fms, mean=%.2fms, ops/sec=%.2f (%d/%d successful)\n",
		result.P50, result.P99, result.Mean, result.OpsPerSec, stats.successful, len(docs))
}

func benchmarkBatchInsert(ctx context.Context, coll *mongo.Collection, dbType string, docs []Cart, volume int, docSize int) {
	fmt.Printf("\n  === Batch Insert Benchmark ===\n")

	// Get IDs of documents to delete
	idsToDelete := make([]string, len(docs))
	for i, doc := range docs {
		idsToDelete[i] = doc.ID
	}

	// Delete documents using batched delete to avoid memory limits
	fmt.Printf("  Deleting %d documents (in batches of %d)...\n", len(idsToDelete), deleteBatchSize)
	deletedCount, err := deleteBatched(ctx, coll, idsToDelete)
	if err != nil {
		log.Fatalf("Failed to delete documents for batch insert test: %v", err)
	}
	fmt.Printf("  ✓ Deleted %d documents\n", deletedCount)

	// Verify deletion completed with retry logic
	deleteFilter := bson.M{"_id": bson.M{"$in": idsToDelete}}
	var count int64
	err = retryOperation(func() error {
		var err error
		count, err = coll.CountDocuments(ctx, deleteFilter)
		return err
	}, maxRetries)
	if err != nil {
		log.Printf("    Warning: Could not verify deletion after %d retries: %v", maxRetries, err)
	} else if count > 0 {
		log.Printf("   Warning: %d documents still exist after deletion\n", count)
	} else {
		fmt.Printf("  ✓ Verified deletion completed\n")
	}

	// Run batch insert benchmark
	var durations []time.Duration
	stats := insertStats{}
	totalDocs := 0
	successfulDocs := 0

	numBatchOps := (len(docs) + batchSize - 1) / batchSize

	progress := NewProgressReporter(numBatchOps, "Batch insert")

	for i := 0; i < len(docs); i += batchSize {
		end := i + batchSize
		if end > len(docs) {
			end = len(docs)
		}

		batch := make([]interface{}, end-i)
		for j := i; j < end; j++ {
			batch[j-i] = docs[j]
		}

		totalDocs += len(batch)
		var duration time.Duration
		batchNum := i / batchSize

		err := retryOperation(func() error {
			start := time.Now()
			_, err := coll.InsertMany(ctx, batch)
			if err == nil || mongo.IsDuplicateKeyError(err) {
				duration = time.Since(start)
				err = nil
			} else {
				log.Printf("  Error inserting batch %d, err: %s", batchNum, err.Error())
			}
			return err
		}, maxRetries)

		if err != nil {
			log.Printf("  Error inserting batch %d after retries: %v", batchNum, err)
			stats.failed++
		} else {
			durations = append(durations, duration)
			stats.successful++
			successfulDocs += len(batch)
		}

		progress.Increment(1)
	}
	progress.Complete()

	if stats.failed > 0 {
		log.Printf("  ⚠️  Batch Insert Stats: %d batches successful, %d batches failed (%d/%d docs inserted)",
			stats.successful, stats.failed, successfulDocs, totalDocs)
	}

	if len(durations) == 0 {
		log.Printf("  No successful batch inserts - skipping metrics")
		return
	}

	result := calculateMetrics("Batch Insert", volume, docSize, dbType, durations)
	results = append(results, result)

	fmt.Printf("  ✓ Batch Insert: p50=%.2fms, p99=%.2fms, mean=%.2fms, ops/sec=%.2f (%d/%d batches successful)\n",
		result.P50, result.P99, result.Mean, result.OpsPerSec, stats.successful, stats.successful+stats.failed)
}

func benchmarkQueries(ctx context.Context, coll *mongo.Collection, dbType string, queryParams []QueryParams, volume int, docSize int) {
	fmt.Printf("\n  === Query Benchmark ===\n")

	// Verify document count with retry logic
	var count int64
	err := retryOperation(func() error {
		var err error
		count, err = coll.CountDocuments(ctx, bson.M{})
		return err
	}, maxRetries)
	if err != nil {
		log.Printf("   Warning: Could not verify document count after %d retries: %v", maxRetries, err)
	} else {
		fmt.Printf("  ✓ Verified %d documents in collection\n", count)
		if count != int64(volume) {
			log.Printf("   Warning: Expected %d documents but found %d", volume, count)
		}
	}

	// Run warmup queries
	fmt.Printf("  Running %d warmup queries...\n", warmupQueryCount)
	for i := 0; i < warmupQueryCount; i++ {
		param := queryParams[i%len(queryParams)]
		runQuerySet(ctx, coll, param)
	}

	// Benchmark queries
	queryTypes := []string{
		"Exact Match",
		// "Range",
		// "AND Condition",
		// "OR Condition",
		// "Array Contains",
		// "Nested Field",
		// "Complex",
	}

	for _, queryType := range queryTypes {
		durations := make([]time.Duration, 0, queryIterations)
		successCount := 0
		skipped := false

		progress := NewProgressReporter(queryIterations, queryType)

		for i := 0; i < queryIterations; i++ {
			param := queryParams[i]

			var duration time.Duration

			// Wrap query execution in retry logic
			err := retryOperation(func() error {
				var start time.Time

				switch queryType {
				case "Exact Match":
					filter := bson.M{"items.id": param.ExactMatchID}
					start = time.Now()
					err := coll.FindOne(ctx, filter).Decode(&Cart{})
					duration = time.Since(start)
					// Ignore "no documents" error as it's not a failure
					if err != nil && err != mongo.ErrNoDocuments {
						return err
					}
					return nil
				case "Range":
					filter := bson.M{"items.price": bson.M{"$gte": param.RangeMinPrice, "$lte": param.RangeMaxPrice}}
					start = time.Now()
					cursor, err := coll.Find(ctx, filter)
					if err != nil {
						return err
					}
					cursor.Close(ctx)
					duration = time.Since(start)
					return nil
				case "AND Condition":
					filter := bson.M{
						"items.quantity": param.AndQuantity,
						"items.grams":    bson.M{"$gte": param.AndMinGrams},
					}
					start = time.Now()
					cursor, err := coll.Find(ctx, filter)
					if err != nil {
						return err
					}
					cursor.Close(ctx)
					duration = time.Since(start)
					return nil
				case "OR Condition":
					filter := bson.M{
						"$or": []bson.M{
							{"items.variant_id": param.OrVariantID1},
							{"items.variant_id": param.OrVariantID2},
						},
					}
					start = time.Now()
					cursor, err := coll.Find(ctx, filter)
					if err != nil {
						return err
					}
					cursor.Close(ctx)
					duration = time.Since(start)
					return nil
				case "Array Contains":
					filter := bson.M{"items": bson.M{"$elemMatch": bson.M{"id": param.ArrayContainsID}}}
					start = time.Now()
					cursor, err := coll.Find(ctx, filter)
					if err != nil {
						return err
					}
					cursor.Close(ctx)
					duration = time.Since(start)
					return nil
				case "Nested Field":
					filter := bson.M{"items.0.price": param.NestedPrice}
					start = time.Now()
					cursor, err := coll.Find(ctx, filter)
					if err != nil {
						return err
					}
					cursor.Close(ctx)
					duration = time.Since(start)
					return nil
				case "Complex":
					filter := bson.M{
						"items.price":      bson.M{"$gte": param.ComplexMinPrice},
						"items.grams":      bson.M{"$lte": param.ComplexMaxGrams},
						"items.variant_id": param.ComplexVariantID,
					}
					start = time.Now()
					cursor, err := coll.Find(ctx, filter)
					if err != nil {
						return err
					}
					cursor.Close(ctx)
					duration = time.Since(start)
					return nil
				}
				return nil
			}, maxRetries)

			if err != nil {
				log.Printf("    %s query %d failed after %d retries: %v - skipping remaining iterations", queryType, i, maxRetries, err)
				skipped = true
				progress.Complete() // Complete the progress bar
				break               // Skip remaining iterations for this query type
			}

			durations = append(durations, duration)
			successCount++

			if (i+1)%10 == 0 {
				progress.Increment(10)
			}
		}

		if !skipped {
			progress.Complete()
		}

		if len(durations) == 0 {
			log.Printf("  %s: No successful queries - skipping metrics", queryType)
			continue
		}

		result := calculateMetrics(queryType, volume, docSize, dbType, durations)
		results = append(results, result)

		if skipped {
			fmt.Printf("  %s: p50=%.2fms, p99=%.2fms, mean=%.2fms, ops/sec=%.2f (%d/%d before skip)\n",
				queryType, result.P50, result.P99, result.Mean, result.OpsPerSec, successCount, queryIterations)
		} else {
			fmt.Printf("  ✓ %s: p50=%.2fms, p99=%.2fms, mean=%.2fms, ops/sec=%.2f (%d/%d successful)\n",
				queryType, result.P50, result.P99, result.Mean, result.OpsPerSec, successCount, queryIterations)
		}
	}
}

// FIX #5: runQuerySet now handles errors properly
func runQuerySet(ctx context.Context, coll *mongo.Collection, param QueryParams) {
	// Exact Match
	filter := bson.M{"items.id": param.ExactMatchID}
	if err := coll.FindOne(ctx, filter).Decode(&Cart{}); err != nil && err != mongo.ErrNoDocuments {
		log.Printf("Warning: Exact match query error: %v", err)
	}

	// Range
	filter = bson.M{"items.price": bson.M{"$gte": param.RangeMinPrice, "$lte": param.RangeMaxPrice}}
	if cursor, err := coll.Find(ctx, filter); err != nil {
		log.Printf("Warning: Range query error: %v", err)
	} else {
		cursor.Close(ctx)
	}

	// AND Condition
	filter = bson.M{
		"items.quantity": param.AndQuantity,
		"items.grams":    bson.M{"$gte": param.AndMinGrams},
	}
	if cursor, err := coll.Find(ctx, filter); err != nil {
		log.Printf("Warning: AND condition query error: %v", err)
	} else {
		cursor.Close(ctx)
	}

	// OR Condition
	filter = bson.M{
		"$or": []bson.M{
			{"items.variant_id": param.OrVariantID1},
			{"items.variant_id": param.OrVariantID2},
		},
	}
	if cursor, err := coll.Find(ctx, filter); err != nil {
		log.Printf("Warning: OR condition query error: %v", err)
	} else {
		cursor.Close(ctx)
	}

	// Array Contains
	filter = bson.M{"items": bson.M{"$elemMatch": bson.M{"id": param.ArrayContainsID}}}
	if cursor, err := coll.Find(ctx, filter); err != nil {
		log.Printf("Warning: Array contains query error: %v", err)
	} else {
		cursor.Close(ctx)
	}

	// Nested Field
	filter = bson.M{"items.0.price": param.NestedPrice}
	if cursor, err := coll.Find(ctx, filter); err != nil {
		log.Printf("Warning: Nested field query error: %v", err)
	} else {
		cursor.Close(ctx)
	}

	// Complex
	filter = bson.M{
		"items.price":      bson.M{"$gte": param.ComplexMinPrice},
		"items.grams":      bson.M{"$lte": param.ComplexMaxGrams},
		"items.variant_id": param.ComplexVariantID,
	}
	if cursor, err := coll.Find(ctx, filter); err != nil {
		log.Printf("Warning: Complex query error: %v", err)
	} else {
		cursor.Close(ctx)
	}
}

func percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	if len(sorted) == 1 {
		return sorted[0]
	}
	// Use linear interpolation for more accurate percentiles
	idx := p / 100.0 * float64(len(sorted)-1)
	lower := int(idx)
	upper := lower + 1
	if upper >= len(sorted) {
		return sorted[len(sorted)-1]
	}
	weight := idx - float64(lower)
	return time.Duration(float64(sorted[lower])*(1-weight) + float64(sorted[upper])*weight)
}

func calculateMetrics(operation string, volume int, docSize int, dbType string, durations []time.Duration) BenchmarkResult {
	if len(durations) == 0 {
		return BenchmarkResult{
			Operation:  operation,
			DataVolume: volume,
			DocSize:    docSize,
			Database:   dbType,
		}
	}

	// Sort durations
	sort.Slice(durations, func(i, j int) bool {
		return durations[i] < durations[j]
	})

	p50 := percentile(durations, 50)
	p80 := percentile(durations, 80)
	p90 := percentile(durations, 90)
	p95 := percentile(durations, 95)
	p99 := percentile(durations, 99)

	var totalDuration time.Duration
	var sum float64
	for _, d := range durations {
		totalDuration += d
		sum += float64(d.Microseconds())
	}
	mean := sum / float64(len(durations))

	// Calculate standard deviation
	var variance float64
	for _, d := range durations {
		diff := float64(d.Microseconds()) - mean
		variance += diff * diff
	}
	stdDev := math.Sqrt(variance / float64(len(durations)))

	// Min and max
	minVal := float64(durations[0].Microseconds()) / 1000.0
	maxVal := float64(durations[len(durations)-1].Microseconds()) / 1000.0

	// Ops per second
	opsPerSec := float64(len(durations)) / totalDuration.Seconds()

	return BenchmarkResult{
		Operation:  operation,
		DataVolume: volume,
		DocSize:    docSize,
		Database:   dbType,
		P50:        float64(p50.Microseconds()) / 1000.0,
		P80:        float64(p80.Microseconds()) / 1000.0,
		P90:        float64(p90.Microseconds()) / 1000.0,
		P95:        float64(p95.Microseconds()) / 1000.0,
		P99:        float64(p99.Microseconds()) / 1000.0,
		Mean:       mean / 1000.0,
		StdDev:     stdDev / 1000.0,
		Min:        minVal,
		Max:        maxVal,
		SampleSize: len(durations),
		OpsPerSec:  opsPerSec,
	}
}

func displayResults() {
	fmt.Println("| Operation | Data Volume | Doc Size | Database | p50 (ms) | p80 (ms) | p90 (ms) | p95 (ms) | p99 (ms) | Mean (ms) | StdDev (ms) | Min (ms) | Max (ms) | Samples | Ops/Sec |")
	fmt.Println("|-----------|-------------|----------|----------|----------|----------|----------|----------|----------|-----------|-------------|----------|----------|---------|---------|")

	for _, result := range results {
		fmt.Printf("| %s | %d | %d | %s | %.2f | %.2f | %.2f | %.2f | %.2f | %.2f | %.2f | %.2f | %.2f | %d | %.2f |\n",
			result.Operation,
			result.DataVolume,
			result.DocSize,
			result.Database,
			result.P50,
			result.P80,
			result.P90,
			result.P95,
			result.P99,
			result.Mean,
			result.StdDev,
			result.Min,
			result.Max,
			result.SampleSize,
			result.OpsPerSec,
		)
	}
}

// FIX #10: Export results to JSON
func exportResultsJSON(outputFile string) error {
	data, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal results: %w", err)
	}

	if outputFile == "" {
		outputFile = fmt.Sprintf("benchmark_results_%s.json", time.Now().Format("20060102_150405"))
	}

	if err := os.WriteFile(outputFile, data, 0644); err != nil {
		return fmt.Errorf("failed to write JSON file: %w", err)
	}

	fmt.Printf("Results exported to: %s\n", outputFile)

	// Also print to stdout for visibility
	fmt.Println(string(data))

	return nil
}

func exportResultsCSV(outputFile string) error {
	if outputFile == "" {
		outputFile = fmt.Sprintf("benchmark_results_%s.csv", time.Now().Format("20060102_150405"))
	}

	file, err := os.Create(outputFile)
	if err != nil {
		return fmt.Errorf("failed to create CSV file: %w", err)
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	defer writer.Flush()

	// Write header
	header := []string{
		"Operation", "DataVolume", "DocSize", "Database",
		"P50_ms", "P80_ms", "P90_ms", "P95_ms", "P99_ms",
		"Mean_ms", "StdDev_ms", "Min_ms", "Max_ms",
		"SampleSize", "OpsPerSec",
	}
	if err := writer.Write(header); err != nil {
		return fmt.Errorf("failed to write CSV header: %w", err)
	}

	// Write data rows
	for _, r := range results {
		row := []string{
			r.Operation,
			fmt.Sprintf("%d", r.DataVolume),
			fmt.Sprintf("%d", r.DocSize),
			r.Database,
			fmt.Sprintf("%.4f", r.P50),
			fmt.Sprintf("%.4f", r.P80),
			fmt.Sprintf("%.4f", r.P90),
			fmt.Sprintf("%.4f", r.P95),
			fmt.Sprintf("%.4f", r.P99),
			fmt.Sprintf("%.4f", r.Mean),
			fmt.Sprintf("%.4f", r.StdDev),
			fmt.Sprintf("%.4f", r.Min),
			fmt.Sprintf("%.4f", r.Max),
			fmt.Sprintf("%d", r.SampleSize),
			fmt.Sprintf("%.4f", r.OpsPerSec),
		}
		if err := writer.Write(row); err != nil {
			return fmt.Errorf("failed to write CSV row: %w", err)
		}
	}

	fmt.Printf("Results exported to: %s\n", outputFile)

	// Also display table for visibility
	displayResults()

	return nil
}

func cleanupCollections(collections []collectionInfo) {
	for _, info := range collections {
		ctx := context.Background()
		client, err := mongo.Connect(ctx, options.Client().ApplyURI(info.uri))
		if err != nil {
			log.Printf("Failed to connect for cleanup: %v", err)
			continue
		}

		coll := client.Database(info.dbName).Collection(info.collection)
		err = retryOperation(func() error {
			return coll.Drop(ctx)
		}, maxRetries)
		if err != nil {
			log.Printf("Failed to drop collection %s after %d retries: %v", info.collection, maxRetries, err)
		} else {
			fmt.Printf("    ✓ Dropped collection: %s\n", info.collection)
		}

		client.Disconnect(ctx)
	}
}

// Cleanup document folders
func cleanupFolders(folders []string) {
	for _, folder := range folders {
		if err := os.RemoveAll(folder); err != nil {
			log.Printf("Failed to remove folder %s: %v", folder, err)
		} else {
			fmt.Printf("    ✓ Removed folder: %s\n", folder)
		}
	}
}
