import fs from 'node:fs';
import path from 'node:path';

export default class VitestResultsReporter {
  onInit(vitest) {
    this.root = vitest.config.root;
  }

  record(entry) {
    fs.appendFileSync(process.env.ATTN_TEST_RESULTS, `${JSON.stringify(entry)}\n`);
  }

  moduleOf(testModule) {
    return path.relative(this.root, testModule.moduleId).split(path.sep).join('/');
  }

  onTestCaseResult(testCase) {
    const diagnostic = testCase.diagnostic();
    this.record({
      type: 'case',
      module: this.moduleOf(testCase.module),
      name: testCase.fullName,
      state: testCase.result().state,
      mode: testCase.options.mode,
      fails: testCase.options.fails === true,
      retryCount: diagnostic?.retryCount ?? 0,
      duration: diagnostic?.duration,
    });
  }

  onTestModuleEnd(testModule) {
    this.record({
      type: 'module',
      module: this.moduleOf(testModule),
      state: testModule.state(),
      errors: testModule.errors().length,
      duration: testModule.diagnostic().duration,
    });
  }

  onTestRunEnd(_testModules, unhandledErrors, reason) {
    this.record({ type: 'end', reason, unhandledErrors: unhandledErrors.length });
  }
}
