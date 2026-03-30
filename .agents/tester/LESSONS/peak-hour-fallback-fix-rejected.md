# Lesson: Peak Hour Fallback Fix REJECTED — Root Cause Still Unknown

**Date**: 2026-03-30
**Status**: INVESTIGATION COMPLETE — No bug found in code
**Original Claim**: Peak hour fallback not working
**Actual Finding**: Original test was misconfigured — fallback model had peak_hour_enabled=true but test expected it to NOT apply peak hour
