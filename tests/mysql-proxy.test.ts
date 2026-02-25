import { describe, it, expect, beforeEach } from 'vitest';
import {
  getLastVerificationError,
  clearVerificationErrors,
  setVerificationError,
  proxyEvents
} from '../src/mysql-proxy';

describe('MySQL Proxy verification errors', () => {
  beforeEach(() => {
    clearVerificationErrors();
  });

  it('should set and get verification errors', () => {
    setVerificationError('test-1', 'Query does not contain expected pattern');
    expect(getLastVerificationError('test-1')).toBe('Query does not contain expected pattern');
  });

  it('should return undefined for unknown test', () => {
    expect(getLastVerificationError('unknown-test')).toBeUndefined();
  });

  it('should clear all errors', () => {
    setVerificationError('test-1', 'Error 1');
    setVerificationError('test-2', 'Error 2');
    clearVerificationErrors();
    expect(getLastVerificationError('test-1')).toBeUndefined();
    expect(getLastVerificationError('test-2')).toBeUndefined();
  });

  it('should update existing error', () => {
    setVerificationError('test-1', 'Old error');
    setVerificationError('test-1', 'New error');
    expect(getLastVerificationError('test-1')).toBe('New error');
  });
});

describe('MySQL Proxy events', () => {
  it('should have proxyEvents emitter', () => {
    expect(proxyEvents).toBeDefined();
    expect(typeof proxyEvents.emit).toBe('function');
    expect(typeof proxyEvents.on).toBe('function');
  });
});
