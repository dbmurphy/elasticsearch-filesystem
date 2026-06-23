// Conventional Commits configuration powering automated versioning.
// feat -> minor, fix -> patch, feat!/BREAKING CHANGE -> major.
module.exports = {
  extends: ['@commitlint/config-conventional'],
  rules: {
    'body-max-line-length': [0, 'always'],
  },
};
