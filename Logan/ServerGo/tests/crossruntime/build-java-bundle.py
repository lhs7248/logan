#!/usr/bin/env python3
"""Bundle compiled original Java + opt-in JUnit test and exact original mapper XML.

Run the CrossRuntimeDatabaseTest Maven compile once first. No Java source,
configuration, database, compatibility golden or fingerprint is changed here.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import xml.etree.ElementTree as ET


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output', required=True, type=Path)
    args = parser.parse_args()
    root = Path(__file__).resolve().parents[2]
    legacy = root / 'tests/legacy-java'
    original = root.parent / 'Server'
    report = legacy / 'target/surefire-reports/TEST-com.meituan.logan.baseline.CrossRuntimeDatabaseTest.xml'
    report_xml = ET.parse(report)
    classpath = next(p.attrib['value'] for p in report_xml.findall('./properties/property')
                     if p.attrib['name'] == 'java.class.path')
    jars = [Path(p) for p in classpath.split(os.pathsep) if p.endswith('.jar')]
    if not jars:
        raise SystemExit('No resolved Maven dependencies in Surefire report')
    out = args.output.resolve()
    out.mkdir(parents=True, exist_ok=False)
    (out / 'lib').mkdir()
    hashes = {}
    for jar in jars:
        shutil.copyfile(jar, out / 'lib' / jar.name)
    for source, target in ((legacy / 'target/classes', out / 'classes'),
                           (legacy / 'target/test-classes', out / 'test-classes'),
                           (original / 'src/main/resources/sqlmap', out / 'mappers')):
        shutil.copytree(source, target)
        for p in target.rglob('*'):
            if p.is_file():
                hashes[str(p.relative_to(out))] = hashlib.sha256(p.read_bytes()).hexdigest()
    # Reproducible provenance demonstrates these XML files were copied verbatim.
    (out / 'provenance.json').write_text(json.dumps({
        'evidence': 'Original Java compiled by existing legacy-java pom; original MyBatis XML copied byte for byte; opt-in test harness',
        'sha256': hashes}, indent=2) + '\n')
    (out / 'run.sh').write_text('#!/bin/sh\nset -eu\n'
        '''case "${CROSSRUNTIME_PHASE:-}" in seed|verify) ;; *) echo 'CROSSRUNTIME_PHASE must be seed or verify' >&2; exit 2;; esac
[ "${CROSSRUNTIME_DISPOSABLE:-}" = YES ] || { echo 'Disposable acknowledgement required' >&2; exit 2; }
: "${CROSSRUNTIME_DIR:?Evidence directory required}"
cd "$(dirname "$0")"
export CROSSRUNTIME_MAPPER_DIR="$PWD/mappers"
'''
        'exec java --add-opens=java.base/java.util=ALL-UNNAMED -cp "classes:test-classes:lib/*" org.junit.runner.JUnitCore com.meituan.logan.baseline.CrossRuntimeDatabaseTest\n')
    (out / 'run.sh').chmod(0o755)
    print(out)


if __name__ == '__main__':
    main()
