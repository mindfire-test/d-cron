---
id: cron-expressions
title: Cron Expression Syntax & Examples
sidebar_label: Cron Expression Guide
---

# ⏰ Cron Expression Syntax & Examples

`d-cron` supports standard 5-field cron syntax, optional 6-field second-level precision syntax, macro descriptors (e.g. `@every 5m`), and explicit timezone overrides (`CRON_TZ`).

---

## 📌 Syntax Overview

A standard cron expression string consists of 5 or 6 space-separated fields:

```
 ┌───────────── second (optional, 0 - 59)
 │ ┌───────────── minute (0 - 59)
 │ │ ┌───────────── hour (0 - 23)
 │ │ │ ┌───────────── day of month (1 - 31)
 │ │ │ │ ┌───────────── month (1 - 12 or JAN-DEC)
 │ │ │ │ │ ┌───────────── day of week (0 - 6 or SUN-SAT)
 │ │ │ │ │ │
 * * * * * *
```

---

## 🕒 Standard Cron Examples

| Cron Expression | Schedule Description | Example Use Case |
| :--- | :--- | :--- |
| `* * * * *` | Every minute | High-frequency queue polling |
| `0 * * * *` | Every hour at minute 0 | Hourly analytics rollup |
| `0 0 * * *` | Every day at midnight (00:00) | Daily data purge / billing settlement |
| `0 2 * * *` | Every day at 02:00 AM | Heavy database vacuum / backup |
| `0 9 * * 1-5` | 09:00 AM, Monday through Friday | Weekday morning notifications |
| `0 0 1 * *` | First day of every month at midnight | Monthly subscription renewals |
| `*/15 * * * *` | Every 15 minutes | Periodic cache warm-up |
| `30 8 15 * *` | 8:30 AM on the 15th of every month | Mid-month invoice generation |

---

## ⏱️ Macro Specifiers

`d-cron` supports human-readable predefined macro expressions:

| Macro | Equivalent Cron | Description |
| :--- | :--- | :--- |
| `@yearly` (or `@annually`) | `0 0 1 1 *` | Run once a year at midnight of Jan 1st |
| `@monthly` | `0 0 1 * *` | Run once a month at midnight of the first day |
| `@weekly` | `0 0 * * 0` | Run once a week at midnight on Sunday |
| `@daily` (or `@midnight`) | `0 0 * * *` | Run once a day at midnight |
| `@hourly` | `0 * * * *` | Run once an hour at the beginning of the hour |
| `@every <duration>` | Custom Duration | Run periodically at exact interval (e.g. `@every 5m`, `@every 1h30m`) |

### Example Using `@every` Macro

```go
// Trigger job every 10 minutes
scheduler.Add("refresh-token-cache", "@every 10m", func(ctx context.Context) error {
    return tokenService.RefreshCache(ctx)
})
```

---

## 🌍 Timezone Support (`CRON_TZ`)

By default, schedules are evaluated against the local system clock of the executing node (`time.Local`).

To specify an explicit IANA timezone (such as `America/New_York` or `Asia/Tokyo`), prefix the cron string with `CRON_TZ=...`:

```go
// Trigger every day at 9:00 AM Eastern Time (New York)
scheduler.Add("us-morning-digest", "CRON_TZ=America/New_York 0 9 * * *", sendDigestHandler)

// Trigger every day at 6:00 PM Japan Standard Time
scheduler.Add("jst-evening-report", "CRON_TZ=Asia/Tokyo 0 18 * * *", jstReportHandler)
```

---

## 🧪 Validating Cron Syntax in Code

You can validate a cron expression programmatically before registering it using the internal parser:

```go
import "github.com/robfig/cron/v3"

parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)
_, err := parser.Parse("0 2 * * *")
if err != nil {
    log.Fatalf("invalid cron expression: %v", err)
}
```
