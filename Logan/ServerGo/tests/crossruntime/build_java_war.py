#!/usr/bin/env python3
"""Stage untouched Java classes/webapp for Tomcat 9 using external test config.

Uses the existing legacy-java compilation/dependency graph (including its modern
Lombok), not the original old Maven build. Writes only a new disposable WAR.
"""
import argparse
import os
from pathlib import Path
import re
import xml.etree.ElementTree as ET
import zipfile


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output', required=True, type=Path)
    args = parser.parse_args()
    if os.environ.get('CROSSRUNTIME_DISPOSABLE') != 'YES':
        raise SystemExit('CROSSRUNTIME_DISPOSABLE=YES required')
    jdbc = os.environ['CROSSRUNTIME_JDBC_URL']
    if not re.fullmatch(r'jdbc:mysql://[^/]+/(?:logan_crossruntime_|logan_reparse_it_)[a-z0-9]+(?:\?.*)?', jdbc):
        raise SystemExit('Only random disposable crossruntime/reparse database permitted')
    root = Path(__file__).resolve().parents[2]
    original = root.parent / 'Server'
    legacy = root / 'tests/legacy-java'
    classes = legacy / 'target/classes'
    report = legacy / 'target/surefire-reports/TEST-com.meituan.logan.baseline.CrossRuntimeDatabaseTest.xml'
    report_xml = ET.parse(report)
    classpath = next(p.attrib['value'] for p in report_xml.findall('./properties/property')
                     if p.attrib['name'] == 'java.class.path')
    jars = [Path(p) for p in classpath.split(os.pathsep) if p.endswith('.jar')]
    if not (classes / 'com/meituan/logan/web/controller/LoganController.class').is_file():
        raise SystemExit('Compile tests/legacy-java before building the WAR')
    # Avoid a servlet-api duplicate in Tomcat and test-only JUnit dependencies.
    jars = [p for p in jars if not any(n in p.name for n in
            ('servlet-api', 'junit-', 'hamcrest-', 'lombok-', 'spring-test-'))]
    config = {'jdbc.driver': 'com.mysql.cj.jdbc.Driver', 'jdbc.url': jdbc,
              'jdbc.username': os.environ['CROSSRUNTIME_DB_USER'],
              'jdbc.password': os.environ['CROSSRUNTIME_DB_PASSWORD']}
    # Java properties escapes: keep ':' '=' intact in values, escape backslashes/newlines.
    db_properties = ''.join(k + '=' + v.replace('\\', '\\\\').replace('\n', '\\n') + '\n'
                            for k, v in config.items())
    # Explicit test OSS fixture endpoint. Never borrow credentials from original config.
    endpoint = os.environ.get('CROSSRUNTIME_OSS_EMULATOR_ENDPOINT', 'http://127.0.0.1:9')
    oss_properties = ('oss.endpoint=' + endpoint + '\noss.accessKeyId=fixture-only\n'
                      'oss.accessKeySecret=fixture-only\noss.bucketName=fixture\n'
                      'oss.prefix=logan-logs/\noss.expireDays=36500\n')
    args.output.parent.mkdir(parents=True, exist_ok=True)
    # Refuse replacement, so a stale or unrelated artifact cannot be clobbered.
    with zipfile.ZipFile(args.output, 'x', zipfile.ZIP_DEFLATED) as war:
        for source, prefix in ((original / 'src/main/webapp', ''),
                               (classes, 'WEB-INF/classes/'),
                               (original / 'src/main/resources', 'WEB-INF/classes/')):
            for p in sorted(source.rglob('*')):
                if not p.is_file():
                    continue
                relative = p.relative_to(source).as_posix()
                if source.name == 'resources' and relative in ('db.properties', 'oss.properties'):
                    continue
                war.write(p, prefix + relative)
        for jar in jars:
            war.write(jar, 'WEB-INF/lib/' + jar.name)
        war.writestr('WEB-INF/classes/db.properties', db_properties)
        war.writestr('WEB-INF/classes/oss.properties', oss_properties)
    os.chmod(args.output, 0o600)
    print(args.output)


if __name__ == '__main__':
    main()
