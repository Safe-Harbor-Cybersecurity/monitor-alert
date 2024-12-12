package main

import (
    "bytes"
    "encoding/json"
    "fmt"
    "log"
    "net/http"
    "os"
    "sync"
    "time"
)

type AlertConfig struct {
    Slack     SlackConfig     `json:"slack"`
    Email     EmailConfig     `json:"email"`
    PagerDuty PagerDutyConfig `json:"pagerduty"`
}

type SlackConfig struct {
    WebhookURL string   `json:"webhook_url"`
    Channels   []string `json:"channels"`
}

type EmailConfig struct {
    SMTPServer string   `json:"smtp_server"`
    SMTPPort   int      `json:"smtp_port"`
    Username   string   `json:"username"`
    Password   string   `json:"password"`
    Recipients []string `json:"recipients"`
}

type PagerDutyConfig struct {
    ServiceKey string `json:"service_key"`
    APIKey     string `json:"api_key"`
}

type ServiceConfig struct {
    Name             string            `json:"name"`
    URL              string            `json:"url"`
    Method           string            `json:"method"`
    Headers          map[string]string `json:"headers"`
    ExpectedStatus   int               `json:"expected_status"`
    Timeout          int              `json:"timeout"`          // in seconds
    CheckInterval    int              `json:"check_interval"`   // in seconds
    RetryAttempts    int              `json:"retry_attempts"`
    RetryDelay       int              `json:"retry_delay"`      // in seconds
    CriticalService  bool             `json:"critical_service"` // If true, triggers immediate paging
}

type MonitorConfig struct {
    Services []ServiceConfig `json:"services"`
    Alerts   AlertConfig    `json:"alerts"`
}

type ServiceStatus struct {
    Name           string
    Status         bool
    LastCheck      time.Time
    LastError      string
    FailureCount   int
    ResponseTime   time.Duration
    AlertSent      bool
    RecoveryTime   *time.Time
}

type Monitor struct {
    config         MonitorConfig
    serviceStatus  map[string]*ServiceStatus
    statusMutex    sync.RWMutex
    httpClient     *http.Client
}

func NewMonitor(configPath string) (*Monitor, error) {
    // Read configuration
    file, err := os.ReadFile(configPath)
    if err != nil {
        return nil, fmt.Errorf("error reading config: %v", err)
    }

    var config MonitorConfig
    if err := json.Unmarshal(file, &config); err != nil {
        return nil, fmt.Errorf("error parsing config: %v", err)
    }

    monitor := &Monitor{
        config:        config,
        serviceStatus: make(map[string]*ServiceStatus),
        httpClient:    &http.Client{},
    }

    // Initialize service status
    for _, service := range config.Services {
        monitor.serviceStatus[service.Name] = &ServiceStatus{
            Name:      service.Name,
            Status:    true,
            LastCheck: time.Now(),
        }
    }

    return monitor, nil
}

func (m *Monitor) checkService(service ServiceConfig) {
    m.statusMutex.Lock()
    status := m.serviceStatus[service.Name]
    m.statusMutex.Unlock()

    startTime := time.Now()
    client := &http.Client{
        Timeout: time.Duration(service.Timeout) * time.Second,
    }

    // Create request
    req, err := http.NewRequest(service.Method, service.URL, nil)
    if err != nil {
        m.updateServiceStatus(service.Name, false, err.Error(), time.Since(startTime))
        return
    }

    // Add headers
    for key, value := range service.Headers {
        req.Header.Add(key, value)
    }

    // Perform the request with retries
    var lastErr error
    for attempt := 0; attempt < service.RetryAttempts; attempt++ {
        resp, err := client.Do(req)
        if err != nil {
            lastErr = err
            time.Sleep(time.Duration(service.RetryDelay) * time.Second)
            continue
        }

        if resp.StatusCode == service.ExpectedStatus {
            m.updateServiceStatus(service.Name, true, "", time.Since(startTime))
            return
        }

        lastErr = fmt.Errorf("unexpected status code: %d", resp.StatusCode)
        resp.Body.Close()
        time.Sleep(time.Duration(service.RetryDelay) * time.Second)
    }

    m.updateServiceStatus(service.Name, false, lastErr.Error(), time.Since(startTime))
}

func (m *Monitor) updateServiceStatus(serviceName string, status bool, errMsg string, responseTime time.Duration) {
    m.statusMutex.Lock()
    defer m.statusMutex.Unlock()

    serviceStatus := m.serviceStatus[serviceName]
    prevStatus := serviceStatus.Status
    serviceStatus.Status = status
    serviceStatus.LastCheck = time.Now()
    serviceStatus.ResponseTime = responseTime

    if !status {
        serviceStatus.LastError = errMsg
        serviceStatus.FailureCount++
        
        if prevStatus && !serviceStatus.AlertSent {
            // Service just went down, send alert
            m.sendAlerts(serviceName, errMsg)
            serviceStatus.AlertSent = true
        }
    } else if !prevStatus && status {
        // Service recovered
        recoveryTime := time.Now()
        serviceStatus.RecoveryTime = &recoveryTime
        serviceStatus.FailureCount = 0
        serviceStatus.AlertSent = false
        m.sendRecoveryAlert(serviceName)
    }
}

func (m *Monitor) sendSlackAlert(service, message string) error {
    payload := map[string]interface{}{
        "text": fmt.Sprintf("🚨 *ALERT*: Service %s is DOWN!\nError: %s\nTime: %s",
            service, message, time.Now().Format(time.RFC3339)),
    }

    jsonPayload, err := json.Marshal(payload)
    if err != nil {
        return err
    }

    resp, err := http.Post(m.config.Alerts.Slack.WebhookURL, "application/json", bytes.NewBuffer(jsonPayload))
    if err != nil {
        return err
    }
    defer resp.Body.Close()

    return nil
}

func (m *Monitor) sendPagerDutyAlert(service, message string) error {
    incident := map[string]interface{}{
        "service_key": m.config.Alerts.PagerDuty.ServiceKey,
        "event_type":  "trigger",
        "description": fmt.Sprintf("Service %s is DOWN - %s", service, message),
        "details": map[string]interface{}{
            "error":     message,
            "timestamp": time.Now().Unix(),
        },
    }

    jsonPayload, err := json.Marshal(incident)
    if err != nil {
        return err
    }

    req, err := http.NewRequest("POST", "https://events.pagerduty.com/v2/enqueue",
        bytes.NewBuffer(jsonPayload))
    if err != nil {
        return err
    }

    req.Header.Set("Content-Type", "application/json")
    req.Header.Set("Authorization", "Token token="+m.config.Alerts.PagerDuty.APIKey)

    resp, err := m.httpClient.Do(req)
    if err != nil {
        return err
    }
    defer resp.Body.Close()

    return nil
}

func (m *Monitor) sendRecoveryAlert(service string) {
    status := m.serviceStatus[service]
    duration := time.Since(*status.RecoveryTime)

    recoveryMsg := fmt.Sprintf("✅ Service %s has RECOVERED\nDowntime: %s\nTime: %s",
        service, duration.Round(time.Second), time.Now().Format(time.RFC3339))

    // Send recovery notifications
    if m.config.Alerts.Slack.WebhookURL != "" {
        payload := map[string]interface{}{"text": recoveryMsg}
        jsonPayload, _ := json.Marshal(payload)
        http.Post(m.config.Alerts.Slack.WebhookURL, "application/json", bytes.NewBuffer(jsonPayload))
    }

    // Resolve PagerDuty incident
    if m.config.Alerts.PagerDuty.ServiceKey != "" {
        incident := map[string]interface{}{
            "service_key": m.config.Alerts.PagerDuty.ServiceKey,
            "event_type":  "resolve",
            "description": fmt.Sprintf("Service %s has recovered", service),
        }
        jsonPayload, _ := json.Marshal(incident)
        http.Post("https://events.pagerduty.com/v2/enqueue", "application/json", bytes.NewBuffer(jsonPayload))
    }
}

func (m *Monitor) sendAlerts(service, message string) {
    // Find service configuration
    var serviceConfig ServiceConfig
    for _, s := range m.config.Services {
        if s.Name == service {
            serviceConfig = s
            break
        }
    }

    // Send Slack alert
    if m.config.Alerts.Slack.WebhookURL != "" {
        if err := m.sendSlackAlert(service, message); err != nil {
            log.Printf("Error sending Slack alert: %v", err)
        }
    }

    // Send PagerDuty alert for critical services
    if serviceConfig.CriticalService && m.config.Alerts.PagerDuty.ServiceKey != "" {
        if err := m.sendPagerDutyAlert(service, message); err != nil {
            log.Printf("Error sending PagerDuty alert: %v", err)
        }
    }
}

func (m *Monitor) startMonitoring() {
    for _, service := range m.config.Services {
        go func(s ServiceConfig) {
            ticker := time.NewTicker(time.Duration(s.CheckInterval) * time.Second)
            for {
                m.checkService(s)
                <-ticker.C
            }
        }(service)
    }
}

func (m *Monitor) startAPIServer() {
    http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
        m.statusMutex.RLock()
        defer m.statusMutex.RUnlock()

        status := make(map[string]interface{})
        for name, s := range m.serviceStatus {
            status[name] = map[string]interface{}{
                "status":        s.Status,
                "last_check":    s.LastCheck,
                "last_error":    s.LastError,
                "failure_count": s.FailureCount,
                "response_time": s.ResponseTime.String(),
            }
        }

        json.NewEncoder(w).Encode(status)
    })

    log.Fatal(http.ListenAndServe(":8080", nil))
}

func main() {
    monitor, err := NewMonitor("monitor_config.json")
    if err != nil {
        log.Fatal(err)
    }

    // Start monitoring routines
    monitor.startMonitoring()

    // Start API server
    monitor.startAPIServer()
}
