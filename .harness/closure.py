"""Task/source-bound evidence reconciliation; not product or authorization judgment."""
import hashlib
import json
from pathlib import Path


def fingerprint(value):
    if not isinstance(value, dict) or value.get('error'):
        return None
    commit, digest = value.get('commit'), value.get('workspace_sha256')
    if not all(isinstance(x, str) and x for x in (commit, digest)):
        return None
    return commit, digest


def file_hash(path):
    digest = hashlib.sha256()
    with path.open('rb') as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b''):
            digest.update(chunk)
    return digest.hexdigest()


def evidence_errors(items):
    errors = []
    if not isinstance(items, list) or not items:
        return ['Missing evidence inventory']
    for item in items:
        try:
            if not isinstance(item, dict) or not isinstance(item.get('path'), str):
                raise ValueError('Invalid evidence entry')
            path = Path(item['path'])
            if not path.is_absolute() or not path.is_file() or file_hash(path) != item.get('sha256'):
                errors.append('Missing or changed evidence: ' + str(path))
        except (OSError, ValueError, TypeError) as exc:
            errors.append(str(exc))
    return errors


def summarize(plan, reports, current, profiles):
    """reports is [(absolute report path, decoded object)]; duplicates fail closed."""
    task = plan.get('task')
    required = list(plan.get('recommended', []))
    required += ['integration:' + row['id'] for row in plan.get('integrations', []) if row['profile'] is None]
    if plan.get('design_acceptance_required'):
        required.append('design')
    result = {'task': task, 'source': current, 'checks': [], 'errors': [],
              'scope': 'Evidence coverage for declared task paths, not complete product acceptance'}
    if not required:
        result['errors'].append('Plan contains no verification requirements')
    expected = fingerprint(plan.get('source'))
    if not isinstance(task, str) or not task.strip():
        result['errors'].append('Plan needs a nonempty task ID')
    if expected is None or expected != fingerprint(current):
        result['errors'].append('Plan source is stale or unbound')
    if plan.get('unmapped'):
        result['errors'].append('Unmapped paths require explicit verification planning')
    groups = {}
    for path, report in reports:
        if not isinstance(report, dict):
            result['errors'].append('Invalid report: ' + str(path)); continue
        kind = report.get('kind')
        key = (report.get('profile') if kind == 'verification' else
               'design' if kind == 'design-acceptance' else
               'integration:' + str(report.get('integration')) if kind == 'external-integration' else None)
        if not isinstance(key, str) or key not in required:
            result['errors'].append('Unexpected report: ' + str(path)); continue
        groups.setdefault(key, []).append((path, report))
    for key in required:
        row = {'check': key, 'status': 'missing'}
        candidates = groups.get(key, [])
        if len(candidates) > 1:
            row.update(status='ambiguous', reason='Supply exactly one report per check')
        elif candidates:
            path, report = candidates[0]
            row['report'] = str(path)
            if report.get('task') != task:
                row.update(status='wrong_task')
            elif fingerprint(report.get('source_start')) != expected or fingerprint(report.get('source_end')) != expected:
                row.update(status='stale')
            elif report.get('status') != 'passed' or report.get('exit_code') != 0:
                row.update(status='failed', reported_status=report.get('status'))
            else:
                errors = evidence_errors(report.get('artifacts'))
                if report.get('kind') == 'verification':
                    steps = report.get('steps')
                    if (not isinstance(steps, list) or any(not isinstance(s, dict) for s in steps)
                            or [s.get('id') for s in steps] != profiles.get(key)
                            or any(s.get('status') != 'passed' or s.get('exit_code') != 0 for s in steps)
                            or report.get('unexecuted') != []):
                        errors.append('Incomplete or failed profile steps')
                elif key == 'design':
                    if report.get('binding') != 'current' or report.get('completeness') != 'complete':
                        errors.append('Design record is incomplete or not current')
                    declared = report.get('declared', {})
                    if (not isinstance(declared, dict) or declared.get('task') != task
                            or (declared.get('code_revision'), declared.get('code_workspace_sha256')) != expected):
                        errors.append('Design declaration binding mismatch')
                else:
                    if not report.get('reviewer') or not report.get('environment'):
                        errors.append('External result needs reviewer and isolated environment description')
                row.update(status='invalid' if errors else 'passed')
                if errors:
                    row['errors'] = errors
        result['checks'].append(row)
    result['status'] = 'passed' if not result['errors'] and all(x['status'] == 'passed' for x in result['checks']) else 'incomplete'
    result['exit_code'] = 0 if result['status'] == 'passed' else 1
    return result
