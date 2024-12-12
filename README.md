# monitor-alert
A comprehensive service monitoring and alerting system.

## Usage 

Create a `monitor_config.json` file:

```json
{
    "services": [
        {
            "name": "API Service",
            "url": "https://api.example.com/health",
            "method": "GET",
            "headers": {
                "Authorization": "Bearer token123"
            },
            "expected_status": 200,
            "timeout": 5,
            "check_interval": 30,
            "retry_attempts": 3,
            "retry_delay": 1,
            "critical_service": true
        }
    ],
    "alerts": {
        "slack": {
            "webhook_url": "https://hooks.slack.com/services/YOUR/WEBHOOK/URL",
            "channels": ["#incidents", "#sre-team"]
        },
        "pagerduty": {
            "service_key": "your-pagerduty-service-key",
            "api_key": "your-pagerduty-api-key"
        }
    }
}
```

Key features:

1. Service Monitoring:
   - Configurable health check endpoints
   - Custom HTTP methods and headers
   - Retry logic with configurable attempts and delays
   - Response time tracking
   - Customizable check intervals

2. Alerting:
   - Slack integration
   - PagerDuty integration for critical services
   - Differentiation between critical and non-critical services
   - Recovery notifications
   - Detailed error reporting

3. Monitoring API:
   - Health check endpoint
   - Service status overview
   - Response time metrics
   - Failure tracking

4. Resilience:
   - Automatic retries
   - Concurrent monitoring
   - Error handling
   - Recovery detection

Remember to:
- Set appropriate timeouts and intervals
- Configure proper authentication for services
- Test the monitoring system itself
- Set up redundancy for critical monitoring
