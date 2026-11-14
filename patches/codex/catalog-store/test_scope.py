import unittest
from configure_store import scope_digest


class ScopeTests(unittest.TestCase):
    def test_all_scope_dimensions_invalidate(self):
        base = ["workspace", "effective-principal", "a" * 64, "b" * 64, "c" * 64]
        original = scope_digest(*base)
        self.assertEqual(original, scope_digest(*base))
        for i in range(len(base)):
            other = list(base)
            other[i] = "other" if i < 2 else "d" * 64
            self.assertNotEqual(original, scope_digest(*other))

    def test_missing_identity_or_invalid_fingerprint_rejected(self):
        for values in [("", "p", "a" * 64, "b" * 64, "c" * 64),
                       ("w", "", "a" * 64, "b" * 64, "c" * 64),
                       ("w", "p", "raw-permissions", "b" * 64, "c" * 64)]:
            with self.assertRaises(ValueError):
                scope_digest(*values)


if __name__ == "__main__":
    unittest.main()
