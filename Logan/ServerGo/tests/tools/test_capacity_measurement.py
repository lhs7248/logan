import importlib.util
from pathlib import Path
import unittest

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location('measurement', ROOT / 'tests/load/measurement.py')
m = importlib.util.module_from_spec(spec)
spec.loader.exec_module(m)


class MeasurementTests(unittest.TestCase):
    def test_cgroup_keeps_rss_distinct_from_total_memory(self):
        raw = 'usage_usec 2000000\nnr_throttled 5\nthrottled_usec 500000\n100000000\n110000000\noom 0\noom_kill 0\n7\nName:\tjava\nVmRSS:\t20000 kB\nThreads:\t7\n'
        x = m.parse_cgroup(raw)
        self.assertEqual(x['memory_current_bytes'], 100000000)
        self.assertEqual(x['process_rss_bytes'], 20480000)
        self.assertEqual(x['pids_current'], 7)
        self.assertEqual(x['oom_kills'], 0)
        with self.assertRaises(ValueError): m.parse_cgroup('usage_usec 1\n')

    def test_cpu_uses_actual_sample_interval_and_excludes_initial_counter(self):
        values = [{'monotonic': 1, 'usage_usec': 7000000, 'memory_current_bytes': 10},
                  {'monotonic': 3, 'usage_usec': 8000000, 'memory_current_bytes': 20},
                  {'monotonic': 7, 'usage_usec': 10000000, 'memory_current_bytes': 15}]
        s = m.resource_summary(values)
        self.assertEqual(s['cpu_core_percent_mean'], 50)
        self.assertEqual(s['memory_current_bytes_peak'], 20)
        self.assertEqual(s['sample_count'], 3)
        with self.assertRaises(ValueError): m.resource_summary(values[::-1])

    def test_final_report_requires_full_window_and_conserved_counts(self):
        r = {'schedule_seconds': 3600, 'elapsed_seconds': 3600.2, 'attempted': 10, 'dropped': 1,
             'workflows': {'Sent': 9, 'Completed': 9, 'Success': 9, 'Errors': 0},
             'http': {'Sent': 45, 'Completed': 45, 'Success': 45, 'Errors': 0}, 'interrupted': False}
        m.validate_report(r, 3600)
        for field, value in [('elapsed_seconds', 3599), ('attempted', 11), ('interrupted', True)]:
            bad = dict(r); bad[field] = value
            with self.subTest(field=field), self.assertRaises(ValueError): m.validate_report(bad, 3600)

    def test_known_java_loss_does_not_become_strict_success(self):
        r = {'http': {'Errors': 3, 'Completed': 3, 'ErrorKinds': {'native_content_incomplete': 3}},
             'workflows': {'Errors': 3, 'ErrorKinds': {'native_content_incomplete': 3}}}
        self.assertEqual(m.classify_errors(r, 'java', 'sdk')['known_content_errors'], 3)
        self.assertEqual(m.classify_errors(r, 'go', 'sdk')['unexpected_errors'], 3)
        bad = {'http': {'Errors': 4, 'Completed': 4, 'ErrorKinds': {'native_content_incomplete': 3, 'timeout': 1}}, 'workflows': r['workflows']}
        self.assertEqual(m.classify_errors(bad, 'java', 'sdk')['unexpected_errors'], 1)

    def test_temporal_coverage_rejects_lost_tail_and_sampling_gap(self):
        start = 1000
        samples = [{'utc': start + second} for second in range(0, 3601, 10)]
        m.validate_coverage(samples, start, start + 3600)
        for bad in [samples[:-10], samples[10:], samples[:100] + samples[110:]]:
            with self.assertRaises(ValueError): m.validate_coverage(bad, start, start + 3600)

    def test_go_variable_nanosecond_timestamps_are_supported_on_python39(self):
        expected = m.parse_utc('2026-10-17T02:38:02.655228Z')
        self.assertAlmostEqual(m.parse_utc('2026-10-17T02:38:02.65522892Z'), expected, places=5)
        self.assertEqual(m.parse_utc('2026-10-17T02:38:02Z'), m.parse_utc('2026-10-17T10:38:02+08:00'))
        with self.assertRaises(ValueError): m.parse_utc('not-a-timestamp')


if __name__ == '__main__': unittest.main()
