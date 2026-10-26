"""Validate declared design evidence completeness, never visual quality."""
import hashlib
import json
from pathlib import Path


def validate(path):
    errors, evidence = [], []
    try:
        doc = json.loads(path.read_text())
        if not isinstance(doc, dict) or doc.get('schema_version') != 1:
            return ['Unsupported acceptance schema'], []
        for field in ('task', 'code_revision', 'reviewer'):
            if not isinstance(doc.get(field), str) or not doc[field].strip():
                errors.append('Missing ' + field)
        expected = doc.get('scope')
        rows = doc.get('records')
        if not isinstance(expected, list) or not expected or not all(isinstance(x, str) and x.strip() for x in expected):
            return errors + ['scope must list page/state IDs'], []
        if len(set(expected)) != len(expected) or not isinstance(rows, list):
            return errors + ['Duplicate scope or invalid records'], []
        seen = set()
        for row in rows:
            if not isinstance(row, dict) or row.get('id') not in expected or row.get('id') in seen:
                errors.append('Unknown or duplicate record ID')
                continue
            seen.add(row['id'])
            for field in ('baseline_revision', 'implementation', 'interaction_result', 'comparison_conditions'):
                if not isinstance(row.get(field), str) or not row[field].strip():
                    errors.append(row['id'] + ': missing ' + field)
            for field in ('functional_result', 'design_result'):
                if row.get(field) != 'passed':
                    errors.append(row['id'] + ': ' + field + ' is not passed')
            if not isinstance(row.get('differences'), list):
                errors.append(row['id'] + ': differences must be a list')
            elif row['differences'] and not row.get('authorization_evidence'):
                errors.append(row['id'] + ': differences require authorization evidence')
            fields = ['baseline', 'design_screenshot', 'implementation_screenshot', 'interaction_evidence']
            if row.get('differences'):
                fields.append('authorization_evidence')
            for field in fields:
                value = row.get(field)
                if not isinstance(value, str) or not value:
                    errors.append(row['id'] + ': missing ' + field)
                    continue
                target = (path.parent / value).resolve()
                if not target.is_file() or target.stat().st_size == 0:
                    errors.append(row['id'] + ': missing/empty evidence ' + field)
                else:
                    evidence.append({'id': row['id'], 'field': field, 'path': str(target),
                                     'sha256': hashlib.sha256(target.read_bytes()).hexdigest()})
        if seen != set(expected):
            errors.append('Missing page/state records: ' + ', '.join(sorted(set(expected) - seen)))
    except (OSError, ValueError, TypeError) as exc:
        errors.append('Invalid acceptance record: ' + str(exc))
    return errors, evidence


def report(path, source_start, source_end, task=None):
    """Preserve declaration and bind it to the exact workspace, not only a commit."""
    import closure
    errors, artifacts = [], []
    declared = {}
    before = None
    try:
        before = closure.file_hash(path)
        declared = json.loads(path.read_text())
        errors, artifacts = validate(path)
        if before != closure.file_hash(path):
            errors.append('Acceptance record changed while being read')
        artifacts.append({'path': str(path.resolve()), 'sha256': before})
    except (OSError, ValueError, TypeError) as exc:
        errors.append(str(exc))
    completeness = 'incomplete' if errors else 'complete'
    current = closure.fingerprint(source_start)
    bound = (isinstance(declared, dict) and current is not None
             and current == closure.fingerprint(source_end)
             and (declared.get('code_revision'), declared.get('code_workspace_sha256')) == current)
    if not bound:
        errors.append('Acceptance declaration does not match current commit and workspace fingerprint')
    declared_task = declared.get('task') if isinstance(declared, dict) else None
    if task is not None and task != declared_task:
        errors.append('Acceptance task ID mismatch')
    return {'kind': 'design-acceptance', 'task': task or declared_task,
            'record': str(path.resolve()), 'record_sha256': before, 'declared': declared,
            'source_start': source_start, 'source_end': source_end,
            'completeness': completeness, 'binding': 'current' if bound else 'stale',
            'errors': errors, 'artifacts': artifacts,
            'status': 'passed' if not errors else 'incomplete', 'exit_code': 1 if errors else 0,
            'scope': 'Declared evidence completeness and source binding; not visual or authorization judgment'}
