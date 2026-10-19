"""Pure capacity evidence checks; cgroup memory is never labelled process RSS."""
from datetime import datetime
import re


def parse_utc(value):
    match = re.fullmatch(r'(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2})(?:\.(\d{1,9}))?(Z|[+-]\d{2}:\d{2})', value)
    if not match:
        raise ValueError('invalid RFC3339 timestamp')
    fraction = ((match[2] or '') + '000000')[:6]
    return datetime.fromisoformat(match[1] + '.' + fraction + match[3].replace('Z', '+00:00')).timestamp()


def validate_coverage(samples, start, end):
    if not samples or samples[0]['utc'] > start + 15 or samples[-1]['utc'] < end - 20:
        raise ValueError('resource measurement missing start or tail')
    for before, after in zip(samples, samples[1:]):
        gap = after['utc'] - before['utc']
        if gap <= 0 or gap > 25:
            raise ValueError('resource measurement gap or clock discontinuity')
    return True


def parse_cgroup(raw):
    counters, scalars = {}, []
    for line in raw.splitlines():
        fields = line.split()
        if not fields:
            continue
        if len(fields) == 1 and fields[0].isdigit():
            scalars.append(int(fields[0]))
        elif len(fields) >= 2 and fields[1].isdigit():
            counters[fields[0].rstrip(':')] = int(fields[1])
    if len(scalars) != 3 or not all(k in counters for k in ('usage_usec', 'oom_kill', 'VmRSS')):
        raise ValueError('incomplete cgroup/process sample')
    return {'usage_usec': counters['usage_usec'], 'nr_throttled': counters.get('nr_throttled', 0),
            'throttled_usec': counters.get('throttled_usec', 0),
            'memory_current_bytes': scalars[0], 'memory_peak_bytes': scalars[1],
            'pids_current': scalars[2], 'process_rss_bytes': counters['VmRSS'] * 1024,
            'threads': counters.get('Threads', 0), 'oom_kills': counters['oom_kill']}


def resource_summary(samples):
    if not samples:
        raise ValueError('no resource samples')
    deltas = []
    for before, after in zip(samples, samples[1:]):
        elapsed = after['monotonic'] - before['monotonic']
        used = after['usage_usec'] - before['usage_usec']
        if elapsed <= 0 or used < 0:
            raise ValueError('non-monotonic resource samples')
        deltas.append((used, elapsed))
    result = {'sample_count': len(samples), 'cpu_core_percent_mean':
              sum(d[0] for d in deltas) / (sum(d[1] for d in deltas) * 10000) if deltas else None,
              'cpu_core_percent_peak': max((used / (seconds * 10000) for used, seconds in deltas), default=None)}
    for key in ('memory_current_bytes', 'memory_peak_bytes', 'process_rss_bytes', 'pids_current', 'threads'):
        values = [v[key] for v in samples if key in v]
        if values:
            result[key + '_peak'] = max(values)
            result[key + '_first'] = values[0]
            result[key + '_last'] = values[-1]
    result['oom_kills'] = max((x.get('oom_kills', 0) for x in samples), default=0)
    result['throttled_usec_delta'] = samples[-1].get('throttled_usec', 0) - samples[0].get('throttled_usec', 0)
    return result


def validate_report(report, seconds):
    if report.get('interrupted') or report['schedule_seconds'] != seconds or report['elapsed_seconds'] < seconds:
        raise ValueError('interrupted or incomplete measurement window')
    for key in ('http', 'workflows'):
        counts = report[key]
        if counts['Sent'] != counts['Completed'] or counts['Completed'] != counts['Success'] + counts['Errors']:
            raise ValueError('request/workflow count conservation failed')
        if sum((counts.get('ErrorKinds') or {}).values()) != counts['Errors']:
            raise ValueError('error category count conservation failed')
    if report['attempted'] != report['workflows']['Sent'] + report['dropped']:
        raise ValueError('offered workflow count conservation failed')
    if report['workflows']['Sent'] == 0 or report['http']['Sent'] == 0:
        raise ValueError('empty workload')


def classify_errors(report, runtime, fixture_mode):
    counts = report['http']
    kinds = counts.get('ErrorKinds') or {}
    known = kinds.get('native_content_incomplete', 0) if runtime == 'java' and fixture_mode == 'sdk' else 0
    return {'strict_errors': counts['Errors'], 'known_content_errors': known,
            'unexpected_errors': counts['Errors'] - known,
            'strict_error_rate': counts['Errors'] / max(1, counts['Completed'])}
