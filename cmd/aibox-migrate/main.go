package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/container"
	"github.com/Tencent/WeKnora/internal/runtime"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

func main() {
	mode := flag.String("mode", "preflight", "prepare or preflight")
	graphitiCommit := flag.String("graphiti-commit", strings.TrimSpace(os.Getenv("AIBOX_GRAPHITI_COMMIT")), "exact Graphiti commit")
	schemaVersion := flag.String("schema-version", strings.TrimSpace(os.Getenv("AIBOX_TEMPORAL_SCHEMA_VERSION")), "temporal extraction schema version")
	modelFingerprint := flag.String("model-fingerprint", strings.TrimSpace(os.Getenv("AIBOX_TEMPORAL_MODEL_FINGERPRINT")), "temporal model/config fingerprint")
	flag.Parse()

	cfg := service.AIBOXMigrationConfig{GraphitiCommit: *graphitiCommit, ExtractionSchemaVersion: *schemaVersion, ModelConfigFingerprint: *modelFingerprint}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	c := container.BuildContainer(runtime.GetContainer())
	var report *service.AIBOXMigrationReport
	err := c.Invoke(func(db *gorm.DB, knowledgeRepo interfaces.KnowledgeRepository, chunkRepo interfaces.ChunkRepository, temporalRepo *apprepo.TemporalRepository, task interfaces.TaskEnqueuer) error {
		m := service.NewAIBOXMigrationService(db, knowledgeRepo, chunkRepo, temporalRepo, task)
		var runErr error
		switch strings.ToLower(strings.TrimSpace(*mode)) {
		case "prepare":
			report, runErr = m.Prepare(ctx, cfg)
		case "preflight":
			report, runErr = m.Preflight(ctx, cfg)
		default:
			return fmt.Errorf("unsupported --mode %q", *mode)
		}
		return runErr
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(report)
	if report != nil && len(report.Blocked) > 0 {
		os.Exit(2)
	}
}
