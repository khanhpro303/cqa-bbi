package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/ai"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/pkg"
)

func groupingJobRequest(role, tenant, method, path, body string, handler gin.HandlerFunc) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("tenant_role", role); c.Set("tenant_id", tenant) })
	r.Handle(method, "/jobs/:jobId", handler)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	return w
}

func TestProductGroupingAdminAndPromptValidationBeforeDatabase(t *testing.T) {
	for _, role := range []string{"", "member", "viewer"} {
		for _, handler := range []gin.HandlerFunc{CreateProductGroupingJob, SaveProductGroupingPrompt} {
			w := groupingJobRequest(role, "tenant", http.MethodPost, "/jobs/group", `{}`, handler)
			if w.Code != http.StatusForbidden {
				t.Fatalf("role %q got %d: %s", role, w.Code, w.Body.String())
			}
		}
	}
	for _, role := range []string{"admin", "owner"} {
		for _, body := range []string{`{`, `{}`, `{"system_prompt":"   "}`, `{"system_prompt":"` + strings.Repeat("a", maxProductGroupingPromptBytes+1) + `"}`} {
			w := groupingJobRequest(role, "tenant", http.MethodPut, "/jobs/group", body, SaveProductGroupingPrompt)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("invalid prompt got %d: %s", w.Code, w.Body.String())
			}
		}
	}
	w := groupingJobRequest("member", "tenant", http.MethodPost, "/jobs/group", `{"name":"Forged task","job_type":"messenger_product_groups","output_schedule":"none","schedule_type":"manual"}`, CreateJob)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("generic create accepted the special type: %d", w.Code)
	}
}

func TestProductGroupingResponseShowsExactEffectivePrompt(t *testing.T) {
	for _, stored := range []string{"", " ", ai.LegacyMessengerProductGroupingPrompt, ai.LegacyMessengerProductGroupingIDsPrompt, "Prompt tùy chỉnh\nGiữ nguyên xuống dòng."} {
		job := models.Job{RulesContent: stored}
		raw, err := json.Marshal(productGroupingJobResponse(job))
		if err != nil {
			t.Fatal(err)
		}
		var payload struct {
			SystemPrompt string `json:"system_prompt"`
		}
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Fatal(err)
		}
		want := stored
		if strings.TrimSpace(stored) == "" || stored == ai.LegacyMessengerProductGroupingPrompt || stored == ai.LegacyMessengerProductGroupingIDsPrompt {
			want = ai.MessengerProductGroupingPrompt
		}
		if payload.SystemPrompt != want {
			t.Fatalf("displayed prompt differs from effective prompt: %q", payload.SystemPrompt)
		}
	}
}

func TestProductGroupingPromptInvalidatesCacheAndRecordsOnlyAILiveRuns(t *testing.T) {
	names := []string{"E-24"}
	provider := &productGroupingMockProvider{response: ai.AIResponse{Content: `{"groups":[{"name":"E-24","members":["E-24"]}]}`}}
	cache := map[string][]serviceQualityProductGroup{}
	starts, finishes, usages := 0, 0, 0
	var lastRunError error
	deps := productGroupingDependencies{
		prompt: "Prompt thứ nhất", cacheScope: "job-one",
		loadCache: func(_ context.Context, _, key string) ([]serviceQualityProductGroup, bool, error) {
			groups, ok := cache[key]
			return groups, ok, nil
		},
		saveCache: func(_ context.Context, _, key string, groups []serviceQualityProductGroup) error {
			cache[key] = groups
			return nil
		},
		aiClient:  func(context.Context, string) (ai.AIProvider, error) { return provider, nil },
		logUsage:  func(context.Context, string, ai.AIResponse) { usages++ },
		startRun:  func(context.Context) error { starts++; return nil },
		finishRun: func(err error) { finishes++; lastRunError = err },
	}
	resolve := func() {
		t.Helper()
		if _, err := resolveProductGroups(context.Background(), "tenant", names, deps); err != nil {
			t.Fatal(err)
		}
	}
	resolve()
	if provider.prompt != deps.prompt {
		t.Fatal("AI did not receive the edited prompt")
	}
	resolve()
	if starts != 1 || finishes != 1 || usages != 1 || lastRunError != nil {
		t.Fatalf("cache should not produce runs/usage: %d %d %d %v", starts, finishes, usages, lastRunError)
	}
	deps.prompt = "Prompt thứ hai"
	resolve()
	if provider.calls != 2 || provider.prompt != deps.prompt {
		t.Fatal("changed prompt reused old cache")
	}
	deps.cacheScope = "recreated-job"
	resolve()
	if provider.calls != 3 {
		t.Fatal("recreated task reused deleted task's cache")
	}
	deps.prompt = "Prompt gây lỗi"
	provider.err = errors.New("AI offline")
	if _, err := resolveProductGroups(context.Background(), "tenant", names, deps); err == nil || lastRunError == nil || starts != 4 || finishes != 4 {
		t.Fatal("failed AI call did not finish its run with an error")
	}
}

func TestMySQLProductGroupingJobLifecycle(t *testing.T) {
	// Uses the existing opt-in helper and a uniquely created/dropped test schema.
	database := facebookOAuthTestDatabase(t)
	if err := database.AutoMigrate(&models.Job{}, &models.JobRun{}, &models.AIUsageLog{}, &models.NotificationLog{}, &models.ActivityLog{}); err != nil {
		t.Fatal(err)
	}
	tenant := pkg.NewUUID()
	createFacebookOAuthTenant(t, database, tenant)
	request := func(role, method, id, body string, handler gin.HandlerFunc) *httptest.ResponseRecorder {
		return groupingJobRequest(role, tenant, method, "/jobs/"+id, body, handler)
	}
	// There is no app_settings table here: disabling must happen before reading policy/cache/AI.
	w := request("member", http.MethodPost, "group", `{}`, GroupServiceQualityProducts)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"enabled":false`) {
		t.Fatalf("disabled task still resolves products: %d %s", w.Code, w.Body.String())
	}
	var wg sync.WaitGroup
	var responses [2]*httptest.ResponseRecorder
	for i := range responses {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			responses[i] = request("admin", http.MethodPost, "group", `{"schedule_type":"cron","rules_content":"forged"}`, CreateProductGroupingJob)
		}(i)
	}
	wg.Wait()
	var created models.Job
	for _, response := range responses {
		if response.Code != http.StatusCreated && response.Code != http.StatusOK {
			t.Fatalf("create failed: %d %s", response.Code, response.Body.String())
		}
		if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
			t.Fatal(err)
		}
	}
	var count int64
	database.Model(&models.Job{}).Where("tenant_id = ? AND job_type = ?", tenant, productGroupingJobType).Count(&count)
	if count != 1 || created.ScheduleType != "manual" || created.Outputs != "[]" || created.RulesContent != ai.MessengerProductGroupingPrompt {
		t.Fatalf("fixed singleton configuration violated: count=%d job=%+v", count, created)
	}
	for _, handler := range []gin.HandlerFunc{UpdateJob, TriggerJob, TestRunJob} {
		w = request("admin", http.MethodPut, created.ID, `{"rules_content":"bypass"}`, handler)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("generic action accepted: %d %s", w.Code, w.Body.String())
		}
	}
	w = request("member", http.MethodDelete, created.ID, "", DeleteJob)
	if w.Code != http.StatusForbidden {
		t.Fatalf("member deleted special task: %d", w.Code)
	}
	w = request("owner", http.MethodPut, created.ID, `{"system_prompt":"Prompt mới: groups name members"}`, SaveProductGroupingPrompt)
	if w.Code != http.StatusOK {
		t.Fatalf("prompt save failed: %d %s", w.Code, w.Body.String())
	}
	job, err := findProductGroupingJob(context.Background(), database, tenant)
	if err != nil || productGroupingPrompt(job) != "Prompt mới: groups name members" {
		t.Fatalf("saved prompt not effective: %v", err)
	}
	deps := defaultProductGroupingDependencies(database, job)
	if err := deps.startRun(context.Background()); err != nil {
		t.Fatal(err)
	}
	deps.logUsage(context.Background(), tenant, ai.AIResponse{Provider: "openai", Model: "gpt-5-mini", InputTokens: 10})
	deps.finishRun(nil)
	var run models.JobRun
	if err := database.Where("job_id = ?", job.ID).First(&run).Error; err != nil || run.Status != "success" || run.FinishedAt == nil {
		t.Fatalf("missing run: %+v %v", run, err)
	}
	var usage models.AIUsageLog
	if err := database.Where("job_id = ?", job.ID).First(&usage).Error; err != nil || usage.JobRunID != run.ID {
		t.Fatalf("usage not linked to task/run: %+v %v", usage, err)
	}
	w = groupingJobRequest("owner", pkg.NewUUID(), http.MethodPut, "/jobs/"+job.ID, `{"system_prompt":"cross tenant"}`, SaveProductGroupingPrompt)
	if w.Code != http.StatusNotFound {
		t.Fatal("cross-tenant prompt edit accepted")
	}
	w = request("admin", http.MethodDelete, job.ID, "", DeleteJob)
	if w.Code != http.StatusOK {
		t.Fatalf("admin delete failed: %d %s", w.Code, w.Body.String())
	}
	deps.finishRun(nil)
	deps.logUsage(context.Background(), tenant, ai.AIResponse{})
	for _, model := range []interface{}{&models.Job{}, &models.JobRun{}, &models.AIUsageLog{}} {
		database.Model(model).Where("tenant_id = ?", tenant).Count(&count)
		if count != 0 {
			t.Fatalf("deleted task/run/usage resurrected: %T %d", model, count)
		}
	}
	w = request("member", http.MethodPost, "group", `{}`, GroupServiceQualityProducts)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"enabled":false`) {
		t.Fatalf("deleted task not disabled: %d %s", w.Code, w.Body.String())
	}
}
