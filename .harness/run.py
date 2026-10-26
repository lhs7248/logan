#!/usr/bin/env python3
"""Agent-only validation entry point; no changes to developer commands."""
import argparse
import json
import re
import subprocess
import sys
import shutil
import unittest
import tempfile
import signal
import os
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
sys.dont_write_bytecode = True
sys.path.insert(0, str(ROOT / ".harness"))
import runtime
import acceptance
import closure


# Changing a documented profile is a code-reviewed contract change, not just JSON.
PROFILES = {
    'self-test': ['harness-self-test'],
    'quick': ['test', 'vet'],
    'full': ['test', 'race', 'vet', 'build'],
    'java': ['java-baseline'],
    'workbench': ['workbench-node', 'workbench-go', 'workbench-build'],
    'workbench-http': ['workbench-build', 'manual-http', 'reading-http', 'push-http'],
}
# Executable HTTP registrations must retain their CLI adapter and profile.
LOCAL_ROUTES = {
    'manual-http': 'Logan/ServerGo/script/verify-workbench-manual-v1.py',
    'reading-http': 'Logan/ServerGo/script/verify-workbench-reading.py',
    'push-http': 'Logan/ServerGo/script/verify-workbench-push-v1.py',
}
SHARED_FILES = {'Logan/ServerGo/' + name for name in ('go.mod', 'go.sum', 'Makefile', 'build.sh', 'Dockerfile')}
SHARED_PREFIXES = ('Logan/ServerGo/internal/config/', 'Logan/ServerGo/internal/protocol/',
                   'Logan/ServerGo/internal/repository/', 'Logan/ServerGo/internal/storage/')

REQUIRED_ASSETS = {
    'AGENTS.md', '.harness/README.md', '.harness/run.py',
    '.harness/manifest.json', '.harness/evals/cases.json',
    '.harness/tests/test_harness.py', '.harness/rules/compatibility.md',
    '.harness/rules/validation.md', '.harness/workflows/bugfix.md',
    '.harness/workflows/migration.md', '.harness/rules/design-fidelity.md',
    '.harness/runtime.py', '.harness/acceptance.py', '.harness/registry.json',
    '.harness/tests/test_extensions.py', '.harness/closure.py',
    '.harness/tests/test_closure.py', '.harness/acceptance-template.json',
}


def string_list(value):
    return (isinstance(value, list) and bool(value)
            and all(isinstance(item, str) and item.strip() for item in value))


def check(root):
    root = root.resolve()
    errors = []
    try:
        manifest = json.loads((root / '.harness/manifest.json').read_text())
        registry = read_registry(root)
        ids = set()
        for item in registry:
            if (not isinstance(item, dict) or not isinstance(item.get('id'), str)
                    or not item['id'].strip() or item['id'] in ids or not string_list(item.get('prefixes'))
                    or not string_list(item.get('requirements'))
                    or item.get('profile') not in (*PROFILES, None)):
                return ['Invalid integration registry']
            ids.add(item['id'])
            script = Path(item['script'])
            if (script.is_absolute() or '..' in script.parts
                    or not (root / script).resolve().is_relative_to(root)
                    or not (root / script).is_file()):
                errors.append('Missing/unsafe registered script: ' + str(script))
        local = {item['id']: item for item in registry if item.get('profile') is not None}
        if set(local) != set(LOCAL_ROUTES):
            errors.append('Executable registry does not match profile steps')
        for name, script in LOCAL_ROUTES.items():
            item = local.get(name, {})
            if item.get('profile') != 'workbench-http' or item.get('script') != script:
                errors.append('Invalid executable route: ' + name)
        cases = json.loads((root / '.harness/evals/cases.json').read_text())
        if not isinstance(manifest, dict) or not string_list(manifest.get('assets')):
            return ['Invalid manifest: assets must be a nonempty string list']
        assets = manifest['assets']
        if len(set(assets)) != len(assets) or not REQUIRED_ASSETS.issubset(assets):
            errors.append('Duplicate or missing required assets')
        if manifest.get('profiles') != PROFILES:
            errors.append('Profiles must match documented quick/full/java checks')
        actual = {'AGENTS.md'}
        for path in (root / '.harness').rglob('*'):
            if '__pycache__' in path.parts:
                continue
            if path.is_file() or path.is_symlink():
                actual.add(path.relative_to(root).as_posix())
        if actual != set(assets):
            errors.append('Asset inventory mismatch: ' + ', '.join(sorted(actual ^ set(assets))))
        for name in assets:
            relative = Path(name)
            path = (root / relative).resolve()
            if (relative.is_absolute() or '..' in relative.parts
                    or not path.is_relative_to(root) or not path.is_file()):
                errors.append('Missing or unsafe asset: ' + name)
                continue
            if path.suffix != '.md':
                continue
            content = path.read_text()
            if re.search(r'/Users/|/home/|[A-Z]:\\', content):
                errors.append('Machine path: ' + name)
            for link in re.findall(r'\]\(([^)]+)\)', content):
                if '://' in link or link.startswith('#'):
                    continue
                target = (path.parent / link.split('#')[0]).resolve()
                if not target.is_relative_to(root) or not target.exists():
                    errors.append('Broken or unsafe link: ' + name + ' -> ' + link)
        if not isinstance(cases, list) or not cases:
            return errors + ['Behavior cases must be a nonempty list']
        seen = set()
        for case in cases:
            if (not isinstance(case, dict) or not isinstance(case.get('id'), str)
                    or not case['id'].strip() or not string_list(case.get('assertions'))
                    or not string_list(case.get('references'))):
                errors.append('Invalid behavior case schema')
                continue
            if case['id'] in seen:
                errors.append('Duplicate case: ' + case['id'])
            seen.add(case['id'])
            for ref in case['references']:
                if ref not in assets:
                    errors.append('Unknown case reference: ' + ref)
    except (OSError, ValueError, KeyError, TypeError) as exc:
        errors.append('Invalid harness configuration: ' + str(exc))
    return errors


def self_test(root):
    suite = unittest.defaultTestLoader.discover(str(root / '.harness/tests'))
    if suite.countTestCases() == 0:
        print('Self-test failed: no tests discovered', file=sys.stderr)
        return 1
    return 0 if unittest.TextTestRunner(verbosity=1).run(suite).wasSuccessful() else 1


def read_registry(root):
    data = json.loads((root / '.harness/registry.json').read_text())
    if not isinstance(data, dict) or not isinstance(data.get('integrations'), list) or not data['integrations']:
        raise ValueError('Invalid registry schema')
    return data['integrations']


execute = runtime.execute


def plan(root, base=None, task=None, selected=None):
    names = set(runtime.git(root, 'diff', '--name-only', '-z', base or 'HEAD').split(b'\0'))
    names.update(runtime.git(root, 'ls-files', '--others', '--exclude-standard', '-z').split(b'\0'))
    paths = sorted(os.fsdecode(x) for x in names if x)
    selection = selected or []
    for prefix in selection:
        if not prefix or Path(prefix).is_absolute() or '..' in Path(prefix).parts:
            raise ValueError('Task paths must be repository-relative files or directories')
    all_paths = paths
    if selection:
        paths = [p for p in paths if any(p == q.rstrip('/') or p.startswith(q.rstrip('/') + '/') for q in selection)]
    profiles, unknown = set(), []
    design = False
    for path in paths:
        if path.startswith('.harness/') or path == 'AGENTS.md':
            profiles.add('self-test')
        elif path.startswith('Logan/ServerGo/'):
            profiles.add('full')
            if path in SHARED_FILES or path.startswith(SHARED_PREFIXES):
                profiles.update(('workbench', 'workbench-http'))
            if '/workbench' in path:
                profiles.add('workbench')
            if '/workbenchui/' in path:
                design = True
        elif path.startswith(('prototypes/', 'docs/product/')):
            design = True
        else:
            unknown.append(path)
    integrations = []
    for item in read_registry(root):
        if any(path == item['script'] or path.startswith(tuple(item['prefixes'])) for path in paths):
            integrations.append(dict(item, status='not_run'))
            if item['profile']:
                profiles.add(item['profile'])
    return {'kind': 'verification-plan', 'task': task, 'selection': selection,
            'out_of_scope': sorted(set(all_paths) - set(paths)),
            'changes': paths, 'base': base or 'HEAD', 'recommended': sorted(profiles),
            'design_acceptance_required': design, 'integrations': integrations,
            'unmapped': unknown, 'scope': 'Conservative suggestions only; no tests executed. Review unmapped paths and task acceptance criteria.'}


def command_for(root, target, output):
    cwd = root / 'Logan/ServerGo'
    if target == 'harness-self-test':
        return [sys.executable, '-B', str(root / '.harness/run.py'), 'self-test'], []
    if target == 'workbench-node':
        tests = sorted(str(p.relative_to(cwd)) for p in (cwd / 'internal/workbenchui').glob('*.test.mjs'))
        if not tests:
            raise ValueError('No workbench Node tests discovered')
        return ['node', '--test', *tests], ['node']
    if target == 'workbench-go':
        return ['go', 'test', '-tags', 'stdjson', './internal/workbench/...', './internal/workbenchui/...', './cmd/workbench/...'], ['go']
    if target == 'workbench-build':
        return ['go', 'build', '-tags', 'stdjson', '-o', str(output / 'workbench'), './cmd/workbench'], ['go']
    if target in ('test', 'race', 'vet', 'build', 'java-baseline'):
        return ['make', target], ['make', 'mvn', 'java'] if target == 'java-baseline' else ['make', 'go']
    local = {item['id']: item for item in read_registry(root) if item['profile'] == 'workbench-http'}
    if target in local:
        if not (output / 'workbench').is_file():
            raise ValueError('Workbench binary unavailable; build must pass first')
        return [sys.executable, str(root / local[target]['script']), '--binary', str(output / 'workbench'), '--report', str(output / (target + '.json'))], []
    raise ValueError('Unknown verification step: ' + target)


def verify(root, profile, timeout=600, task=None):
    output = Path(tempfile.mkdtemp(prefix='logan-agent-'))
    report = {'kind': 'verification', 'task': task, 'profile': profile, 'scope': 'Selected checks only; not complete product acceptance',
              'started_at': runtime.now(), 'status': 'running', 'exit_code': None, 'steps': [],
              'unexecuted': list(PROFILES.get(profile, []))}
    destination = output / 'summary.json'
    def persist():
        runtime.save(destination, report)
    persist()
    code = 1
    try:
        report['source_start'] = runtime.identity(root)
        report['tools'] = runtime.versions()
        report['preflight_errors'] = check(root)
        if profile not in PROFILES:
            report['preflight_errors'].append('Unknown profile')
        persist()
        if report['preflight_errors']:
            report['status'] = 'blocked'
            return 1
        code = 0
        for target in PROFILES[profile]:
            log = output / (target + '.log')
            step = {'id': target, 'cwd': 'Logan/ServerGo', 'log': str(log),
                    'status': 'running', 'started_at': runtime.now()}
            report['steps'].append(step)
            report['unexecuted'].remove(target)
            persist()
            print('Running: ' + target, flush=True)
            try:
                command, required = command_for(root, target, output)
                step['command'] = command
                missing = [tool for tool in required if shutil.which(tool) is None]
                if missing:
                    step.update(status='blocked', exit_code=None, reason='Missing tools: ' + ', '.join(missing))
                    log.write_text(step['reason'])
                else:
                    step.update(execute(command, root / 'Logan/ServerGo', log, timeout=timeout))
            except ValueError as exc:
                step.update(status='blocked', exit_code=None, reason=str(exc))
            except KeyboardInterrupt:
                step.update(status='interrupted', exit_code=None)
                raise
            except Exception:
                step.update(status='error', exit_code=None)
                raise
            finally:
                step['ended_at'] = runtime.now()
                persist()
            print(target + ': ' + step['status'], flush=True)
            if step['status'] != 'passed':
                code = 1
        report['status'] = 'passed' if code == 0 else 'failed'
    except KeyboardInterrupt:
        code = 130
        report['status'] = 'interrupted'
    except Exception as exc:
        code = 1
        report.update(status='error', error=type(exc).__name__ + ': ' + str(exc))
    finally:
        report['source_end'] = runtime.identity(root)
        report['source_changed_during_run'] = report.get('source_start') != report['source_end']
        if code == 0 and (report['source_changed_during_run'] or 'error' in report['source_end']):
            code = 1
            report['status'] = 'stale' if report['source_changed_during_run'] else 'unbound'
        report['ended_at'] = runtime.now()
        report['exit_code'] = code
        report['artifacts'] = [{'path': str(p.resolve()), 'sha256': closure.file_hash(p)}
                               for p in sorted(output.glob('*.log')) if p.is_file()]
        persist()
        print('Evidence: ' + str(destination), flush=True)
    return code


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=['check', 'self-test', 'verify', 'plan', 'acceptance', 'summarize'])
    parser.add_argument('--profile', choices=list(PROFILES), default='quick')
    parser.add_argument('--timeout', type=float, default=600, help='Per-command timeout in seconds')
    parser.add_argument('--base', help='Git revision for plan; default HEAD plus working changes')
    parser.add_argument('--record', type=Path, help='Design acceptance JSON record')
    parser.add_argument('--task', help='Stable task ID shared by plan and reports')
    parser.add_argument('--path', action='append', default=[], help='Explicit task file/directory scope for plan; repeatable')
    parser.add_argument('--output', type=Path, help='New plan JSON path; existing files are never overwritten')
    parser.add_argument('--plan', type=Path, help='Saved plan to reconcile')
    parser.add_argument('--report', action='append', type=Path, default=[], help='Report JSON to reconcile; repeatable')
    args = parser.parse_args()
    if args.task is not None and not args.task.strip():
        parser.error('--task cannot be blank')
    if not 0 < args.timeout < float('inf'):
        parser.error('--timeout must be finite and positive')
    if args.action == 'verify':
        return verify(ROOT, args.profile, args.timeout, args.task)
    errors = check(ROOT)
    if errors:
        print('\n'.join(errors), file=sys.stderr)
        return 1
    if args.action == 'plan':
        before = runtime.identity(ROOT)
        result = plan(ROOT, args.base, args.task, args.path)
        after = runtime.identity(ROOT)
        if closure.fingerprint(before) is None or before != after:
            raise ValueError('Cannot bind plan: source changed or unavailable')
        result.update(source=after, created_at=runtime.now())
        if args.output:
            if not args.task:
                parser.error('--task is required when saving a plan')
            # Reports/plans belong outside source state to avoid invalidating themselves.
            if args.output.resolve().is_relative_to(ROOT):
                parser.error('--output must be outside the repository')
            with args.output.open('x') as stream:
                os.chmod(args.output, 0o600)
                json.dump(result, stream, ensure_ascii=False, indent=2)
            print('Plan: ' + str(args.output.resolve()))
        else:
            print(json.dumps(result, ensure_ascii=False, indent=2))
        return 0
    if args.action == 'acceptance':
        if not args.record:
            parser.error('--record is required')
        source = runtime.identity(ROOT)
        result = acceptance.report(args.record.resolve(), source, source, args.task)
        after = runtime.identity(ROOT)
        result['source_end'] = after
        if closure.fingerprint(source) != closure.fingerprint(after):
            result.update(status='stale', binding='stale', exit_code=1)
            result['errors'].append('Source changed during acceptance validation')
        result['checked_at'] = runtime.now()
        output = Path(tempfile.mkdtemp(prefix='logan-acceptance-')) / 'summary.json'
        runtime.save(output, result)
        print('Evidence: ' + str(output))
        for error in result['errors']:
            print(error, file=sys.stderr)
        return result['exit_code']
    if args.action == 'summarize':
        if not args.plan:
            parser.error('--plan is required')
        plan_hash = closure.file_hash(args.plan)
        saved = json.loads(args.plan.read_text())
        if not isinstance(saved, dict) or saved.get('kind') != 'verification-plan':
            raise ValueError('Invalid verification plan')
        if args.task is not None and args.task != saved.get('task'):
            raise ValueError('Summary task differs from plan task')
        current = runtime.identity(ROOT)
        # Recompute suggestions: deleting required rows from saved JSON cannot weaken coverage.
        expected = plan(ROOT, saved.get('base'), saved.get('task'), saved.get('selection'))
        inputs = [{'path': str(p.resolve()), 'sha256': closure.file_hash(p)} for p in args.report]
        rows = [(p.resolve(), json.loads(p.read_text())) for p in args.report]
        result = closure.summarize(saved, rows, current, PROFILES)
        for field in ('changes', 'recommended', 'integrations', 'design_acceptance_required', 'unmapped', 'out_of_scope'):
            if saved.get(field) != expected[field]:
                result['errors'].append('Plan no longer matches routing: ' + field)
        result['errors'].extend(closure.evidence_errors(inputs) if inputs else [])
        if plan_hash != closure.file_hash(args.plan):
            result['errors'].append('Plan changed during reconciliation')
        after = runtime.identity(ROOT)
        if current != after:
            result['errors'].append('Source changed during reconciliation')
        if result['errors']:
            result.update(status='incomplete', exit_code=1)
        result.update(checked_at=runtime.now(), plan=str(args.plan.resolve()),
                      plan_sha256=plan_hash, inputs=inputs, selection=saved.get('selection'),
                      out_of_scope=saved.get('out_of_scope'))
        output = Path(tempfile.mkdtemp(prefix='logan-closure-')) / 'summary.json'
        runtime.save(output, result)
        print(json.dumps(result, ensure_ascii=False, indent=2))
        print('Evidence: ' + str(output))
        return result['exit_code']
    print('Harness check: passed', flush=True)
    return self_test(ROOT) if args.action == 'self-test' else 0


if __name__ == '__main__':
    sys.dont_write_bytecode = True
    # SIGTERM from task cancellation gets the same cleanup/evidence path as Ctrl-C.
    def interrupted(*_):
        raise KeyboardInterrupt
    signal.signal(signal.SIGTERM, interrupted)
    try:
        sys.exit(main())
    except (OSError, ValueError, TypeError, KeyError, RuntimeError) as exc:
        print('Harness error: ' + str(exc), file=sys.stderr)
        sys.exit(1)
