package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/kayungou/BatchManagementofCloudServerAccounts/internal/digitalocean"
	"github.com/kayungou/BatchManagementofCloudServerAccounts/internal/model"
	"github.com/kayungou/BatchManagementofCloudServerAccounts/internal/security"
	"github.com/kayungou/BatchManagementofCloudServerAccounts/internal/store"
)

type Worker struct {
	store       *store.Store
	security    *security.Manager
	concurrency int
	poll        time.Duration
	syncEvery   time.Duration
	logger      *slog.Logger
}

func New(store *store.Store, securityManager *security.Manager, concurrency int, poll, syncEvery time.Duration, logger *slog.Logger) *Worker {
	return &Worker{store: store, security: securityManager, concurrency: concurrency, poll: poll, syncEvery: syncEvery, logger: logger}
}

func (w *Worker) Run(ctx context.Context) error {
	workerID := uuid.New()
	hostname, _ := os.Hostname()
	var group sync.WaitGroup
	for i := 0; i < w.concurrency; i++ {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			w.loop(ctx, index)
		}(i)
	}
	group.Add(1)
	go func() {
		defer group.Done()
		w.scheduler(ctx)
	}()
	group.Add(1)
	go func() {
		defer group.Done()
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			_ = w.store.WorkerHeartbeat(ctx, workerID, hostname, os.Getpid())
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	<-ctx.Done()
	group.Wait()
	return ctx.Err()
}

func (w *Worker) loop(ctx context.Context, index int) {
	ticker := time.NewTicker(w.poll)
	defer ticker.Stop()
	for {
		if err := w.ProcessNext(ctx); err != nil && !errors.Is(err, store.ErrNotFound) && !errors.Is(err, context.Canceled) {
			w.logger.Error("worker job failed", "worker", index, "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (w *Worker) scheduler(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		if err := w.store.EnqueueStaleSyncs(ctx, w.syncEvery); err != nil && !errors.Is(err, context.Canceled) {
			w.logger.Error("enqueue scheduled account syncs", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (w *Worker) ProcessNext(ctx context.Context) error {
	job, err := w.store.ClaimJob(ctx)
	if err != nil {
		return err
	}
	result, state, actionIDs, processErr := w.process(ctx, job)
	if processErr != nil {
		message := processErr.Error()
		if state == "" {
			state = "failed"
		}
		_ = w.store.UpdateJob(context.WithoutCancel(ctx), job.ID, state, 100, result, actionIDs, &message)
		return fmt.Errorf("job %s (%s): %w", job.ID, job.Kind, processErr)
	}
	if state == "" {
		state = "succeeded"
	}
	return w.store.UpdateJob(ctx, job.ID, state, 100, result, actionIDs, nil)
}

func (w *Worker) process(ctx context.Context, job model.Job) (any, string, []int64, error) {
	switch job.Kind {
	case "sync_account":
		if job.AccountID == nil {
			return nil, "failed", nil, errors.New("missing account ID")
		}
		return w.syncAccount(ctx, *job.AccountID)
	case "create_droplets":
		return w.createDroplets(ctx, job)
	case "droplet_action":
		return w.dropletAction(ctx, job)
	case "delete_droplets":
		return w.deleteDroplets(ctx, job)
	default:
		return nil, "failed", nil, fmt.Errorf("unknown job kind %q", job.Kind)
	}
}

func (w *Worker) accountClient(ctx context.Context, accountID uuid.UUID) (model.CloudAccount, *digitalocean.Client, error) {
	account, err := w.store.GetCloudAccount(ctx, accountID)
	if err != nil {
		return model.CloudAccount{}, nil, err
	}
	plaintext, err := w.security.Decrypt(account.TokenCiphertext, account.TokenNonce, "digitalocean_token")
	if err != nil {
		return account, nil, fmt.Errorf("decrypt DigitalOcean token: %w", err)
	}
	return account, digitalocean.New(string(plaintext)), nil
}

func (w *Worker) syncAccount(ctx context.Context, accountID uuid.UUID) (any, string, []int64, error) {
	account, client, err := w.accountClient(ctx, accountID)
	if err != nil {
		if account.ID != uuid.Nil {
			_ = w.store.MarkAccountValidationError(ctx, account.ID, "invalid", err.Error())
		}
		return nil, "failed", nil, err
	}
	providerAccount, rate, err := client.Account(ctx)
	if err != nil {
		status := "invalid"
		if digitalocean.IsStatus(err, http.StatusForbidden) {
			status = "insufficient"
		}
		_ = w.store.MarkAccountValidationError(ctx, account.ID, status, err.Error())
		return nil, "failed", nil, err
	}

	providerID := providerAccount.UUID
	if providerAccount.Team != nil && providerAccount.Team.UUID != "" {
		providerID = providerAccount.Team.UUID
	}
	limits := map[string]int{"droplet_limit": providerAccount.DropletLimit, "floating_ip_limit": providerAccount.FloatingIPLimit}
	validation := store.AccountValidation{
		ProviderAccountID: providerID,
		ProviderEmail:     providerAccount.Email,
		ProviderStatus:    providerAccount.Status,
		StatusMessage:     providerAccount.StatusMessage,
		CredentialStatus:  "valid",
		AccountLimits:     limits,
		RateRemaining:     rate.Remaining,
		RateResetAt:       rate.ResetAt,
	}
	if balance, _, balanceErr := client.Balance(ctx); balanceErr == nil {
		validation.AccountBalance = parseFloat(balance.AccountBalance)
		validation.MonthToDateUsage = parseFloat(balance.MonthToDateUsage)
		validation.MonthToDateBalance = parseFloat(balance.MonthToDateBalance)
	} else if !digitalocean.IsStatus(balanceErr, http.StatusForbidden) {
		w.logger.Warn("DigitalOcean balance sync failed", "account", account.ID, "error", balanceErr)
	}
	if err := w.store.UpdateAccountValidation(ctx, account.ID, validation); err != nil {
		return nil, "failed", nil, fmt.Errorf("update account validation: %w", err)
	}
	account.ProviderAccountID = &providerID

	remoteDroplets, _, err := client.ListDroplets(ctx)
	if err != nil {
		return nil, "failed", nil, err
	}
	droplets := make([]model.Droplet, 0, len(remoteDroplets))
	for _, remote := range remoteDroplets {
		droplets = append(droplets, mapDroplet(account, remote))
	}
	if err := w.store.UpsertDroplets(ctx, account, droplets); err != nil {
		return nil, "failed", nil, err
	}
	if err := w.store.MarkAccountSynced(ctx, account.ID); err != nil {
		return nil, "failed", nil, err
	}
	return map[string]any{"account_id": account.ID, "droplets": len(droplets)}, "succeeded", nil, nil
}

type createPayload struct {
	Names            []string       `json:"names"`
	Region           string         `json:"region"`
	Size             string         `json:"size"`
	Image            any            `json:"image"`
	SSHKeys          []any          `json:"ssh_keys"`
	Backups          bool           `json:"backups"`
	BackupPolicy     map[string]any `json:"backup_policy"`
	IPv6             bool           `json:"ipv6"`
	Monitoring       bool           `json:"monitoring"`
	Tags             []string       `json:"tags"`
	VPCUUID          string         `json:"vpc_uuid"`
	ProjectID        string         `json:"project_id"`
	AuthMode         string         `json:"auth_mode"`
	PublicNetworking *bool          `json:"public_networking"`
}

func (w *Worker) createDroplets(ctx context.Context, job model.Job) (any, string, []int64, error) {
	if job.AccountID == nil {
		return nil, "failed", nil, errors.New("missing account ID")
	}
	var payload createPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return nil, "failed", nil, err
	}
	if len(payload.Names) == 0 || len(payload.Names) > 10 {
		return nil, "failed", nil, errors.New("one to ten names are required")
	}

	account, client, err := w.accountClient(ctx, *job.AccountID)
	if err != nil {
		return nil, "failed", nil, err
	}
	unlock, err := w.lockUser(ctx, account.UserID)
	if err != nil {
		return nil, "failed", nil, err
	}
	defer unlock()
	if _, _, _, err := w.syncAccount(ctx, account.ID); err != nil {
		return nil, "failed", nil, fmt.Errorf("refresh account before create: %w", err)
	}
	if err := w.checkQuota(ctx, account.UserID, client, payload.Size, len(payload.Names)); err != nil {
		return nil, "failed", nil, err
	}

	createdIDs := make([]int64, 0, len(payload.Names))
	actionIDs := make([]int64, 0, len(payload.Names))
	passwords := map[int64]string{}
	failures := make([]map[string]any, 0)

	createOne := func(names []string, password string) {
		request := digitalocean.CreateDropletRequest{
			Names: names, Region: payload.Region, Size: payload.Size, Image: payload.Image,
			SSHKeys: payload.SSHKeys, Backups: payload.Backups, BackupPolicy: payload.BackupPolicy,
			IPv6: payload.IPv6, Monitoring: payload.Monitoring, Tags: payload.Tags,
			VPCUUID: payload.VPCUUID, PublicNetworking: payload.PublicNetworking,
		}
		if password != "" {
			request.UserData = digitalocean.BuildRootPasswordCloudInit(password)
			request.SSHKeys = nil
		}
		response, _, createErr := client.CreateDroplets(ctx, request)
		if createErr != nil {
			failures = append(failures, map[string]any{"names": names, "error": createErr.Error()})
			return
		}
		for _, droplet := range response.Droplets {
			createdIDs = append(createdIDs, droplet.ID)
			if password != "" {
				passwords[droplet.ID] = password
			}
		}
		for _, action := range response.Actions {
			actionIDs = append(actionIDs, action.ID)
			if action.ResourceID != 0 {
				if _, waitErr := client.WaitDropletAction(ctx, action.ResourceID, action.ID, 12*time.Minute); waitErr != nil {
					failures = append(failures, map[string]any{"action_id": action.ID, "error": waitErr.Error()})
				}
			}
		}
	}

	if payload.AuthMode == "root_password" {
		for _, name := range payload.Names {
			password, passwordErr := security.GenerateRootPassword(24)
			if passwordErr != nil {
				failures = append(failures, map[string]any{"name": name, "error": passwordErr.Error()})
				continue
			}
			createOne([]string{name}, password)
		}
	} else {
		createOne(payload.Names, "")
	}

	projectAssigned := false
	if len(createdIDs) > 0 && payload.ProjectID != "" {
		if _, _, assignErr := client.AssignProject(ctx, payload.ProjectID, createdIDs); assignErr != nil {
			failures = append(failures, map[string]any{"project_id": payload.ProjectID, "error": assignErr.Error(), "note": "droplets were created and retained"})
		} else {
			projectAssigned = true
		}
	}
	if _, _, _, syncErr := w.syncAccount(ctx, account.ID); syncErr != nil {
		failures = append(failures, map[string]any{"sync": syncErr.Error()})
	}
	if projectAssigned {
		failures = append(failures, persistDropletProjects(createdIDs, payload.ProjectID, func(providerID int64, projectID string) error {
			return w.store.SetDropletProject(ctx, account.ID, providerID, projectID)
		})...)
	}
	for providerID, password := range passwords {
		droplet, getErr := w.store.GetDropletByProviderID(ctx, account.ID, providerID)
		if getErr != nil {
			failures = append(failures, map[string]any{"droplet_id": providerID, "credential": getErr.Error()})
			continue
		}
		ciphertext, nonce, encryptErr := w.security.Encrypt([]byte(password), "root_password:"+droplet.ID.String())
		if encryptErr != nil {
			failures = append(failures, map[string]any{"droplet_id": providerID, "credential": encryptErr.Error()})
			continue
		}
		if saveErr := w.store.UpsertRootCredential(ctx, account.UserID, droplet.ID, ciphertext, nonce); saveErr != nil {
			failures = append(failures, map[string]any{"droplet_id": providerID, "credential": saveErr.Error()})
		}
	}
	result := map[string]any{"created_ids": createdIDs, "failures": failures}
	if len(createdIDs) == 0 {
		return result, "failed", actionIDs, errors.New("no droplets were created")
	}
	if len(failures) > 0 {
		return result, "partial", actionIDs, errors.New("one or more create steps failed")
	}
	return result, "succeeded", actionIDs, nil
}

type actionPayload struct {
	DropletIDs []int64        `json:"droplet_ids"`
	Action     string         `json:"action"`
	Parameters map[string]any `json:"parameters"`
}

func (w *Worker) dropletAction(ctx context.Context, job model.Job) (any, string, []int64, error) {
	if job.AccountID == nil {
		return nil, "failed", nil, errors.New("missing account ID")
	}
	var payload actionPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return nil, "failed", nil, err
	}
	allowed := map[string]bool{"power_on": true, "power_off": true, "power_cycle": true, "shutdown": true, "reboot": true,
		"password_reset": true, "resize": true, "rebuild": true, "rename": true, "snapshot": true,
		"enable_backups": true, "disable_backups": true, "restore": true}
	if !allowed[payload.Action] || !validDropletIDs(payload.DropletIDs, 100) {
		return nil, "failed", nil, errors.New("unsupported or empty droplet action")
	}
	if (payload.Action == "rename" || payload.Action == "resize" || payload.Action == "rebuild" || payload.Action == "restore") && len(payload.DropletIDs) != 1 {
		return nil, "failed", nil, fmt.Errorf("%s only supports one droplet per request", payload.Action)
	}
	account, client, err := w.accountClient(ctx, *job.AccountID)
	if err != nil {
		return nil, "failed", nil, err
	}
	actionIDs := make([]int64, 0, len(payload.DropletIDs))
	failures := make([]map[string]any, 0)
	for _, dropletID := range payload.DropletIDs {
		request := buildDropletActionRequest(payload.Action, payload.Parameters, dropletID, time.Now())
		action, _, actionErr := client.DropletAction(ctx, dropletID, request)
		if actionErr != nil {
			failures = append(failures, map[string]any{"droplet_id": dropletID, "error": actionErr.Error()})
			continue
		}
		actionIDs = append(actionIDs, action.ID)
		if _, waitErr := client.WaitDropletAction(ctx, dropletID, action.ID, 20*time.Minute); waitErr != nil {
			failures = append(failures, map[string]any{"droplet_id": dropletID, "action_id": action.ID, "error": waitErr.Error()})
		}
		if payload.Action == "password_reset" || payload.Action == "rebuild" {
			if droplet, getErr := w.store.GetDropletByProviderID(ctx, account.ID, dropletID); getErr == nil {
				_ = w.store.MarkRootCredentialStale(ctx, droplet.ID)
			}
		}
	}
	_, _, _, syncErr := w.syncAccount(ctx, account.ID)
	if syncErr != nil {
		failures = append(failures, map[string]any{"sync": syncErr.Error()})
	}
	result := map[string]any{"action": payload.Action, "droplet_ids": payload.DropletIDs, "failures": failures}
	if len(failures) > 0 {
		return result, "partial", actionIDs, errors.New("one or more actions failed")
	}
	return result, "succeeded", actionIDs, nil
}

func (w *Worker) deleteDroplets(ctx context.Context, job model.Job) (any, string, []int64, error) {
	if job.AccountID == nil {
		return nil, "failed", nil, errors.New("missing account ID")
	}
	var payload struct {
		DropletIDs []int64 `json:"droplet_ids"`
	}
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return nil, "failed", nil, err
	}
	if !validDropletIDs(payload.DropletIDs, 100) {
		return nil, "failed", nil, errors.New("one to one hundred droplet IDs are required")
	}
	account, client, err := w.accountClient(ctx, *job.AccountID)
	if err != nil {
		return nil, "failed", nil, err
	}
	failures := make([]map[string]any, 0)
	deleted := make([]int64, 0)
	for _, id := range payload.DropletIDs {
		if _, deleteErr := client.DeleteDroplet(ctx, id); deleteErr != nil {
			failures = append(failures, map[string]any{"droplet_id": id, "error": deleteErr.Error()})
		} else {
			deleted = append(deleted, id)
		}
	}
	_, _, _, _ = w.syncAccount(ctx, account.ID)
	result := map[string]any{"deleted_ids": deleted, "failures": failures}
	if len(deleted) == 0 && len(failures) > 0 {
		return result, "failed", nil, errors.New("no droplets were deleted")
	}
	if len(failures) > 0 {
		return result, "partial", nil, errors.New("one or more deletes failed")
	}
	return result, "succeeded", nil, nil
}

func buildDropletActionRequest(action string, parameters map[string]any, dropletID int64, now time.Time) map[string]any {
	request := make(map[string]any, len(parameters)+1)
	for key, value := range parameters {
		if key != "type" {
			request[key] = value
		}
	}
	request["type"] = action
	if action == "snapshot" {
		if _, ok := request["name"]; !ok {
			request["name"] = fmt.Sprintf("snapshot-%d-%s", dropletID, now.UTC().Format("20060102-150405"))
		}
	}
	return request
}

func persistDropletProjects(providerIDs []int64, projectID string, persist func(int64, string) error) []map[string]any {
	failures := make([]map[string]any, 0)
	for _, providerID := range providerIDs {
		if err := persist(providerID, projectID); err != nil {
			failures = append(failures, map[string]any{
				"droplet_id":   providerID,
				"project_id":   projectID,
				"local_record": err.Error(),
			})
		}
	}
	return failures
}

func validDropletIDs(providerIDs []int64, maximum int) bool {
	if len(providerIDs) == 0 || len(providerIDs) > maximum {
		return false
	}
	seen := make(map[int64]struct{}, len(providerIDs))
	for _, providerID := range providerIDs {
		if providerID <= 0 {
			return false
		}
		if _, exists := seen[providerID]; exists {
			return false
		}
		seen[providerID] = struct{}{}
	}
	return true
}

func (w *Worker) checkQuota(ctx context.Context, userID uuid.UUID, client *digitalocean.Client, sizeSlug string, count int) error {
	user, err := w.store.GetUserByID(ctx, userID)
	if err != nil {
		return err
	}
	currentDroplets, currentVCPUs, currentMemory, err := w.store.UserUsage(ctx, userID)
	if err != nil {
		return err
	}
	if user.QuotaDroplets != nil && currentDroplets+count > *user.QuotaDroplets {
		return fmt.Errorf("droplet quota exceeded: %d existing + %d requested > %d", currentDroplets, count, *user.QuotaDroplets)
	}
	if user.QuotaVCPUs == nil && user.QuotaMemoryMB == nil {
		return nil
	}
	raw, _, err := client.GetRaw(ctx, "/sizes", url.Values{"per_page": []string{"200"}})
	if err != nil {
		return fmt.Errorf("load sizes for quota check: %w", err)
	}
	var response struct {
		Sizes []digitalocean.Size `json:"sizes"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return err
	}
	var selected *digitalocean.Size
	for i := range response.Sizes {
		if response.Sizes[i].Slug == sizeSlug {
			selected = &response.Sizes[i]
			break
		}
	}
	if selected == nil {
		return fmt.Errorf("unknown size %q", sizeSlug)
	}
	if user.QuotaVCPUs != nil && currentVCPUs+selected.VCPUs*count > *user.QuotaVCPUs {
		return fmt.Errorf("vCPU quota exceeded")
	}
	if user.QuotaMemoryMB != nil && currentMemory+selected.Memory*count > *user.QuotaMemoryMB {
		return fmt.Errorf("memory quota exceeded")
	}
	return nil
}

func (w *Worker) lockUser(ctx context.Context, userID uuid.UUID) (func(), error) {
	connection, err := w.store.Pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	key := userID.String()
	if _, err := connection.Exec(ctx, `SELECT pg_advisory_lock(hashtext($1))`, key); err != nil {
		connection.Release()
		return nil, err
	}
	return func() {
		_, _ = connection.Exec(context.Background(), `SELECT pg_advisory_unlock(hashtext($1))`, key)
		connection.Release()
	}, nil
}

func mapDroplet(account model.CloudAccount, remote digitalocean.Droplet) model.Droplet {
	image, _ := json.Marshal(remote.Image)
	features, _ := json.Marshal(remote.Features)
	var ipv4, ipv6 *string
	for _, network := range remote.Networks.V4 {
		if network.Type == "public" && network.IPAddress != "" {
			value := network.IPAddress
			ipv4 = &value
			break
		}
	}
	for _, network := range remote.Networks.V6 {
		if network.Type == "public" && network.IPAddress != "" {
			value := network.IPAddress
			ipv6 = &value
			break
		}
	}
	region, size, vpc := stringPointer(remote.Region.Slug), stringPointer(remote.SizeSlug), stringPointer(remote.VPCUUID)
	return model.Droplet{
		UserID: account.UserID, AccountID: account.ID, ProviderID: remote.ID, Name: remote.Name,
		Status: remote.Status, Locked: remote.Locked, RegionSlug: region, SizeSlug: size,
		VCPUs: remote.VCPUs, MemoryMB: remote.Memory, DiskGB: remote.Disk,
		PriceHourly: &remote.Size.PriceHourly, PriceMonthly: &remote.Size.PriceMonthly,
		IPv4: ipv4, IPv6: ipv6, Image: image, Features: features, Tags: remote.Tags, VPCUUID: vpc, Raw: remote.Raw,
	}
}

func parseFloat(value string) *float64 {
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return nil
	}
	return &parsed
}

func stringPointer(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return &value
}

var _ = pgx.ErrNoRows
