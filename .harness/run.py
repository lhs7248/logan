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
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent


# Changing a documented profile is a code-reviewed contract change, not just JSON.
PROFILES = {
    'quick': ['test', 'vet'],
    'full': ['test', 'race', 'vet', 'build'],
    'java': ['java-baseline'],
}
REQUIRED_ASSETS = {
    'AGENTS.md', '.harness/README.md', '.harness/run.py',
    '.harness/manifest.json', '.harness/evals/cases.json',
    '.harness/tests/test_harness.py', '.harness/rules/compatibility.md',
    '.harness/rules/validation.md', '.harness/workflows/bugfix.md',
    '.harness/workflows/migration.md',
}


def string_list(value):
    return (isinstance(value, list) and bool(value)
            and all(isinstance(item, str) and item.strip() for item in value))


def check(root):
    root = root.resolve()
    errors = []
    try:
        manifest = json.loads((root / '.harness/manifest.json').read_text())
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


def execute(command, cwd, log):
    try:
        with log.open('w') as stream:
            result = subprocess.run(command, cwd=cwd, stdout=stream, stderr=subprocess.STDOUT)
        return {'status': 'passed' if result.returncode == 0 else 'failed', 'exit_code': result.returncode}
    except OSError as exc:
        log.write_text(str(exc))
        return {'status': 'blocked', 'exit_code': None}


def verify(root, profile):
    errors = check(root)
    if errors or profile not in PROFILES:
        print('Verification blocked: ' + '; '.join(errors or ['Unknown profile']), file=sys.stderr)
        return 1
    output = Path(tempfile.mkdtemp(prefix='logan-agent-'))
    report = {'profile': profile, 'scope': 'Selected commands only; not integration or release acceptance', 'steps': []}
    code = 0
    try:
        for target in PROFILES[profile]:
            command = ['make', target]
            log = output / (target + '.log')
            print('Running: ' + ' '.join(command), flush=True)
            step = {'command': command, 'cwd': 'Logan/ServerGo', 'log': str(log)}
            report['steps'].append(step)
            try:
                required = ['make', 'mvn', 'java'] if target == 'java-baseline' else ['make', 'go']
                missing = [tool for tool in required if shutil.which(tool) is None]
                if missing:
                    log.write_text('Missing tools: ' + ', '.join(missing) + '\n')
                    step.update(status='blocked', exit_code=None, reason='Missing tools: ' + ', '.join(missing))
                    code = 1
                    print(target + ': blocked', flush=True)
                    continue
                step.update(execute(command, root / 'Logan/ServerGo', log))
            except KeyboardInterrupt:
                step.update(status='interrupted', exit_code=None)
                raise
            print(target + ': ' + step['status'], flush=True)
            if step['status'] != 'passed':
                code = 1
        return code
    except KeyboardInterrupt:
        code = 130
        return code
    except Exception as exc:
        code = 1
        report['error'] = type(exc).__name__ + ': ' + str(exc)
        if report['steps'] and 'status' not in report['steps'][-1]:
            report['steps'][-1].update(status='error', exit_code=None)
        return code
    finally:
        report['exit_code'] = code
        report['unexecuted'] = PROFILES[profile][len(report['steps']):]
        (output / 'summary.json').write_text(json.dumps(report, indent=2) + '\n')
        print('Evidence: ' + str(output / 'summary.json'), flush=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=['check', 'self-test', 'verify'])
    parser.add_argument('--profile', choices=['quick', 'full', 'java'], default='quick')
    args = parser.parse_args()
    errors = check(ROOT)
    for error in errors:
        print(error, file=sys.stderr)
    if errors:
        return 1
    print('Harness check: passed', flush=True)
    if args.action == 'self-test':
        return self_test(ROOT)
    return verify(ROOT, args.profile) if args.action == 'verify' else 0


if __name__ == '__main__':
    sys.dont_write_bytecode = True
    sys.exit(main())
