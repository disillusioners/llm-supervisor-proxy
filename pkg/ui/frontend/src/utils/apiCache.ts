/**
 * Lightweight cache with TTL and request deduplication
 * for frontend API calls.
 */

export const DEFAULT_TTL = 30_000; // 30 seconds

interface CacheEntry<T> {
  value: T;
  expiresAt: number;
  promise?: Promise<T>;
}

/**
 * Cache options
 */
interface CacheOptions {
  /** Default TTL in milliseconds */
  ttl?: number;
  /** Enable periodic sweep for cleanup */
  sweepInterval?: number;
  /** Called when an entry is evicted due to TTL */
  onEvict?: (key: string) => void;
}

type Debug = typeof console.debug;

const defaultDebug: Debug = () => {};

/**
 * Lightweight TTL cache with request deduplication
 */
export class APICache<T = unknown> {
  private cache = new Map<string, CacheEntry<T>>();
  private readonly defaultTTL: number;
  private readonly debug: Debug;
  private readonly onEvict?: (key: string) => void;
  private sweepTimer?: ReturnType<typeof setInterval>;

  constructor(options: CacheOptions = {}) {
    this.defaultTTL = options.ttl ?? DEFAULT_TTL;
    this.onEvict = options.onEvict;
    // Simple debug that does nothing - caller can override if needed
    this.debug = defaultDebug;

    if (options.sweepInterval && options.sweepInterval > 0) {
      this.startSweep(options.sweepInterval);
    }
  }

  /**
   * Get a value from cache. Returns undefined if not found or expired.
   * If expired, triggers async refresh if a fetcher is provided.
   */
  get(key: string): T | undefined {
    const entry = this.cache.get(key);
    if (!entry) {
      this.debug(`MISS: ${key}`);
      return undefined;
    }

    if (this.isExpired(entry)) {
      this.debug(`EXPIRED: ${key}`);
      this.delete(key);
      return undefined;
    }

    this.debug(`HIT: ${key}`);
    return entry.value;
  }

  /**
   * Get or fetch a value with deduplication.
   * If a request is already in-flight for this key, returns the same promise.
   * @param key Cache key
   * @param fetcher Function to fetch the value if not cached
   * @param ttl Optional TTL override for this specific entry
   */
  async getOrFetch<U = T>(key: string, fetcher: () => Promise<U>, ttl?: number): Promise<U> {
    // Check for existing in-flight request
    const existing = this.cache.get(key);
    if (existing?.promise) {
      this.debug(`DEDUP: ${key} (reusing in-flight request)`);
      return existing.promise as unknown as Promise<U>;
    }

    // Check for valid cached value
    if (existing && !this.isExpired(existing)) {
      this.debug(`HIT: ${key}`);
      return existing.value as unknown as U;
    }

    // Create new request
    this.debug(`FETCH: ${key}`);
    const promise = fetcher()
      .then((value) => {
        this.set(key, value as unknown as T, ttl);
        // Clear the promise reference once complete (allows re-fetch)
        const entry = this.cache.get(key);
        if (entry) {
          entry.promise = undefined;
        }
        return value;
      })
      .catch((error) => {
        // Clear the promise on error so next call can retry
        const entry = this.cache.get(key);
        if (entry) {
          entry.promise = undefined;
        }
        throw error;
      });

    // Store the promise for deduplication
    if (existing) {
      (existing as CacheEntry<T>).promise = promise as unknown as Promise<T>;
    } else {
      this.cache.set(key, {
        value: undefined as T,
        expiresAt: 0,
        promise: promise as unknown as Promise<T>,
      });
    }

    return promise;
  }

  /**
   * Set a value in cache
   * @param key Cache key
   * @param value Value to cache
   * @param ttl Optional TTL override (defaults to instance TTL)
   */
  set(key: string, value: T, ttl?: number): void {
    const effectiveTTL = ttl ?? this.defaultTTL;
    const entry: CacheEntry<T> = {
      value,
      expiresAt: Date.now() + effectiveTTL,
    };
    this.cache.set(key, entry);
    this.debug(`SET: ${key} (ttl=${effectiveTTL}ms)`);
  }

  /**
   * Delete a specific key from cache
   */
  delete(key: string): boolean {
    const existed = this.cache.has(key);
    if (existed) {
      this.cache.delete(key);
      this.onEvict?.(key);
      this.debug(`DELETE: ${key}`);
    }
    return existed;
  }

  /**
   * Invalidate keys matching a prefix pattern
   * @param prefix Key prefix to match
   * @returns Number of keys invalidated
   */
  deleteByPrefix(prefix: string): number {
    let count = 0;
    for (const key of this.cache.keys()) {
      if (key.startsWith(prefix)) {
        this.delete(key);
        count++;
      }
    }
    if (count > 0) {
      this.debug(`DELETE_BY_PREFIX: ${prefix} (${count} keys)`);
    }
    return count;
  }

  /**
   * Clear all entries from cache
   */
  clear(): number {
    const count = this.cache.size;
    this.cache.clear();
    this.debug(`CLEAR: ${count} keys`);
    return count;
  }

  /**
   * Get cache stats
   */
  stats(): { size: number; keys: string[] } {
    return {
      size: this.cache.size,
      keys: Array.from(this.cache.keys()),
    };
  }

  /**
   * Check if an entry is expired
   */
  private isExpired(entry: CacheEntry<T>): boolean {
    return Date.now() >= entry.expiresAt;
  }

  /**
   * Remove expired entries from cache
   * @returns Number of entries removed
   */
  sweep(): number {
    const now = Date.now();
    let count = 0;
    for (const [key, entry] of this.cache.entries()) {
      if (now >= entry.expiresAt) {
        this.cache.delete(key);
        this.onEvict?.(key);
        count++;
      }
    }
    if (count > 0) {
      this.debug(`SWEEP: removed ${count} expired entries`);
    }
    return count;
  }

  /**
   * Start periodic sweep cleanup
   * @param intervalMs Sweep interval in milliseconds
   */
  startSweep(intervalMs: number): void {
    this.stopSweep();
    this.sweepTimer = setInterval(() => this.sweep(), intervalMs);
  }

  /**
   * Stop periodic sweep
   */
  stopSweep(): void {
    if (this.sweepTimer) {
      clearInterval(this.sweepTimer);
      this.sweepTimer = undefined;
    }
  }

  /**
   * Cleanup resources (call when disposing)
   */
  destroy(): void {
    this.stopSweep();
    this.clear();
  }
}

// Default instance for simple use cases
export const defaultAPICache = new APICache();
