# aws-multi-log-inspector

Console app to fetch AWS CloudWatch Logs entries across multiple log groups tied to a given string and print them to stdout.

## Requirements

- Go 1.25+
- AWS credentials with `logs:FilterLogEvents` permission on the target log groups
- AWS region configured (env `AWS_REGION`, profile, or other default sources)

## Install

```
go build ./cmd/aws-multi-log-inspector
```

## Usage

```
aws-multi-log-inspector [--groups g1,g2] [--region ap-northeast-1] --profile your-profile <X-Request-Id>
```

- `--groups`: Comma-separated CloudWatch Log Group names. Alternatively set env `LOG_GROUP_NAMES`.
- `--region`: AWS region (optional). Falls back to AWS SDK defaults if omitted.
- `--profile`: AWS shared config profile. If omitted, the app uses env `AWS_PROFILE`. One of them is required.

Output format (one line per log event):

```
<RFC3339 timestamp> <log-group>/<log-stream> <message>
```

## Notes

- Implementation uses `FilterLogEvents` per group and merges results sorted by timestamp.
- The search window is fixed to the last 24 hours.
- Output events are sorted chronologically (ascending by timestamp).
- If a log message is valid JSON (object/array), it is pretty-printed with indentation. Otherwise, the raw string is printed.
- If no matching events are found, the tool prints: "No logs found for the given X-Request-Id in the last 24h." and exits successfully.
