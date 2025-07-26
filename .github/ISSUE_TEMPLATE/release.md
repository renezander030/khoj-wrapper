---
name: Release Request
about: Request a new release with specific version
title: 'Release v[VERSION]'
labels: 'release'
assignees: ''

---

## Release Information

**Version:** v[X.Y.Z]
**Release Type:** [ ] Major [ ] Minor [ ] Patch [ ] Pre-release

## Changes in This Release

### New Features
- [ ] Feature 1
- [ ] Feature 2

### Bug Fixes
- [ ] Fix 1
- [ ] Fix 2

### Improvements
- [ ] Improvement 1
- [ ] Improvement 2

### Breaking Changes
- [ ] Breaking change 1 (if any)

## Pre-Release Checklist

- [ ] All tests pass
- [ ] Documentation updated
- [ ] CHANGELOG.md updated
- [ ] Version bumped in relevant files
- [ ] Local build test completed (`scripts/build-all.sh`)

## Release Process

1. **Create and push tag:**
   ```bash
   git tag v[X.Y.Z]
   git push origin v[X.Y.Z]
   ```

2. **Verify automated build:**
   - [ ] GitHub Actions workflow triggered
   - [ ] All platform builds successful
   - [ ] Binaries uploaded to release
   - [ ] Checksums generated

3. **Post-release:**
   - [ ] Release notes reviewed and updated
   - [ ] Download links tested
   - [ ] Documentation updated with new version

## Additional Notes

[Any additional information about this release]
