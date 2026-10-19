"""Sanitized claimed-task stage summaries. Publication contains decode/insert;
queue is first-attempt DB enqueue-to-owned-snapshot, not summed with execution.
No-op SQL claims do not emit an event. Repeated attempts remain explicit.
"""
import calendar
import time
import re
import collections
import json
import math

REQUIRED_STAGES={'download','decompress','parse_stage','stage_decode','db_insert','publication'}
STAGES={'queue','download','decompress','parse_stage','stage_decode','db_insert','publication'}
OUTCOMES={'none','deadline','cancelled','mysql_1213','mysql_1205','mysql_1062','mysql_1064','mysql_other','other'}
def number(v):
    if type(v) not in (int,float) or not math.isfinite(v) or v<0:
        raise ValueError('invalid processing duration')
    return v

def distribution(values):
    values=sorted(values)
    return dict(count=len(values),mean=sum(values)/len(values),p50=values[math.ceil(len(values)*.50)-1],p95=values[math.ceil(len(values)*.95)-1],p99=values[math.ceil(len(values)*.99)-1],max=values[-1])

def utc_ns(value):
    match=re.fullmatch(r'(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2})(?:\.(\d{1,9}))?(?:Z|\+00:00)',value)
    if not match:raise ValueError('UTC stage timestamp required')
    seconds=calendar.timegm(time.strptime(match[1],'%Y-%m-%dT%H:%M:%S'))
    return seconds*1000000000+int((match[2] or '').ljust(9,'0'))

def summarize(text,expected_task_ids,start_utc=None,end_utc=None):
    if (start_utc is None)!=(end_utc is None):raise ValueError('both stage bounds required')
    bounds=(utc_ns(start_utc),utc_ns(end_utc)) if start_utc is not None else None
    if bounds and bounds[0]>bounds[1]:raise ValueError('invalid stage window')
    expected=set(expected_task_ids);rows=[];counts=collections.Counter();outcomes=collections.Counter();stages=collections.defaultdict(list)
    for line in text.splitlines():
        try: value=json.loads(line)
        except (ValueError,TypeError):continue
        if not isinstance(value,dict) or value.get('msg')!='native processing stages':continue
        if bounds and not bounds[0]<=utc_ns(value.get('time',''))<=bounds[1]:continue
        task=value.get('task_id');outcome=value.get('outcome');timing=value.get('stages_ms')
        if type(task) is not int or task<=0 or task not in expected or outcome not in OUTCOMES or not isinstance(timing,dict) or not set(timing)<=STAGES:
            raise ValueError('invalid or unexpected processing event')
        duration=number(value.get('duration_ms'))
        timing={key:number(v) for key,v in timing.items()}
        rows.append(dict(task_id=task,outcome=outcome,duration_ms=duration,stages_ms=timing))
        counts[task]+=1;outcomes[outcome]+=1
        for key,v in timing.items():stages[key].append(v)
    missing=sorted(expected-set(counts));repeated=sorted(task for task,n in counts.items() if n>1)
    event_complete=bool(expected) and not missing and not repeated and set(outcomes)=={'none'}
    missing_stages=[dict(task_id=row['task_id'],stages=sorted(REQUIRED_STAGES-set(row['stages_ms']))) for row in rows if row['outcome']=='none' and not REQUIRED_STAGES<=set(row['stages_ms'])]
    segmented=event_complete and not missing_stages
    return dict(complete=segmented,event_accounting_complete=event_complete,segmentation_complete=segmented,missing_stages=missing_stages,
                expected_tasks=len(expected),claimed_tasks=len(counts),claimed_attempts=len(rows),missing_task_ids=missing,repeated_task_ids=repeated,outcomes=dict(outcomes),
                duration_ms=distribution([r['duration_ms'] for r in rows]) if rows else None,
                stages_ms={key:distribution(v) for key,v in sorted(stages.items())},samples=rows,
                accounting='claimed native attempts only; publication includes stage_decode and db_insert; queue includes claim/snapshot overhead, uses DB clock on first attempt only; do not sum nested stages; missing/error/repeated attempts explicit; supplied logs must be restricted to stage time window')

def validate_e2e(report):
    """Fail closed for a successful formal run; failed samples stay in raw report."""
    result={}
    for flow in ('native','web'):
        expected={row['Cycle']:row['TaskID'] for row in report['ledger'] if row['Flow']==flow}
        value=report['upload_to_all_details'][flow];samples=value['Samples']
        if not expected or value.get('Final') is not True or value.get('ActiveUploads')!=0 or value.get('Censored')!=0 or value.get('Queryable')!=len(expected) or value.get('CompletedObservations')!=len(expected) or len(samples)!=len(expected):
            raise ValueError('incomplete final upload-to-details accounting')
        observed={}
        for sample in samples:
            cycle=sample['Cycle'];task=sample['TaskID'];elapsed=number(sample['ElapsedMS'])
            if type(cycle) is not int or cycle in observed or expected.get(cycle)!=task or sample['Queryable'] is not True or sample.get('Failure') or elapsed<=0:
                raise ValueError('upload-to-details sample disagrees with successful ledger')
            observed[cycle]=elapsed
        if set(observed)!=set(expected):raise ValueError('upload-to-details sample missing')
        summary=distribution(list(observed.values()))
        if number(value['P95MS'])!=summary['p95'] or number(value['P99MS'])!=summary['p99']:
            raise ValueError('upload-to-details percentiles disagree with samples')
        result[flow]=summary
    return result
