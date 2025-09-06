# aws-multi-log-inspector

Console app to fetch AWS CloudWatch Logs entries across multiple log groups matching a CloudWatch Logs filter pattern and print them to stdout.

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
aws-multi-log-inspector \
  --filter-pattern <pattern> \
  [--groups g1,g2] \
  [--region ap-northeast-1] \
  [--profile your-profile] \
  [--start RFC3339] [--end RFC3339] \
  [--extract name=jmespath --next-filter jmes-or-literal] [--pretty] \
  [--concurrency N]
```

- `--groups`: Comma-separated CloudWatch Log Group names. Alternatively set env `LOG_GROUP_NAMES`.
- `--region`: AWS region (optional). Falls back to AWS SDK defaults if omitted.
- `--profile`: AWS shared config profile (optional). If omitted, the app first uses env `AWS_PROFILE` when present; if that still doesn’t resolve, it falls back to environment credentials (`AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, optional `AWS_SESSION_TOKEN`) and region from `--region` or `AWS_REGION`.
- `--filter-pattern`: Search pattern (required). See [Filter and Pattern Syntax](https://docs.aws.amazon.com/AmazonCloudWatch/latest/logs/FilterAndPatternSyntax.html).
- `--start`/`--end`: Override the time window in RFC3339. If both omitted, last 24h is used. If only `--start` is set, the end is `start+24h`. If only `--end` is set, the start is `end-24h`.
- `--extract`: Extract a value from the first search results using JMESPath: `name=path`. For non-JSON messages, the raw text is available as `message`.
- `--next-filter`: Build a second filter using JMESPath evaluated against `{ "value": <extracted> }`, or treat the argument as a literal if not valid JMESPath. You can also embed the extracted value via `{{name}}`, which will be JSON-quoted safely before evaluation.
- `--pretty`: Pretty-print JSON. Both the first and second search results are output as an indented JSON array of records.
- `--concurrency`: Number of parallel log-group searches (default: 4). Automatically bounded by the number of groups. Increasing this may speed up queries but can increase API pressure.

Output format (first search; one line per log event when not using `--pretty`):

```
<RFC3339 timestamp> <log-group>/<log-stream> <message>
```

If `--pretty` is set and there are results, the first search results are output as an indented JSON array (same as the second search).

## Notes

- Implementation uses `FilterLogEvents` per group and merges results, then sorts chronologically (ascending by timestamp).
- Concurrency: searches are executed in parallel across groups (default workers: 4). On the first error, remaining requests are canceled to reduce wasted work.
- Credentials resolution order when creating the client:
  1) Shared config/profile (with `--profile` or `AWS_PROFILE`), honoring `--region` if provided.
  2) Environment variables: `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, optional `AWS_SESSION_TOKEN`; region via `--region` or `AWS_REGION`.
  3) Otherwise, the AWS SDK’s default resolution chain applies.
- The default search window is the last 24 hours; it can be overridden with `--start`/`--end`.
- If no matching events are found, the tool prints: `No logs found for the given pattern "<pattern>" in the last 24h.` and exits successfully.

## Two-Phase Search (Extract and Re-search)

Examples:

1) Extract a user ID from the first search, then use it for a second search across groups:

```
--filter-pattern "ERROR" \
--extract "userID=user.id" \
--next-filter "userId={{userID}}"
```

2) Extract a substring from non-JSON messages and build an `@message` equality filter for the second search:

```
--filter-pattern "WARN" \
--extract "value=message" \
--next-filter "join('', ['@message = \"', {{value}}, '\"'])" \
--pretty
```

The second search results are output as JSON (use `--pretty` for indented output). The first search uses the same JSON format when `--pretty` is enabled.

## Credential Examples

- Use a shared config profile in a specific region:

```
AWS_PROFILE=dev aws-multi-log-inspector --region ap-northeast-1 --groups "/aws/lambda/app" --filter-pattern "ERROR"
```

- Use static environment credentials (access key/secret) with a region:

```
export AWS_ACCESS_KEY_ID=...
export AWS_SECRET_ACCESS_KEY=...
export AWS_SESSION_TOKEN=...   # optional
aws-multi-log-inspector --region ap-northeast-1 --groups "/aws/ecs/service" --filter-pattern "WARN"
```
