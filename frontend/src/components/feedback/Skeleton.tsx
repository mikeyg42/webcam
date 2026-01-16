import { cn } from '../../lib/utils';

export interface SkeletonProps {
  /** Width (CSS value or Tailwind class) */
  width?: string;
  /** Height (CSS value or Tailwind class) */
  height?: string;
  /** Make circular */
  circle?: boolean;
  /** CSS class */
  className?: string;
}

export function Skeleton({
  width,
  height,
  circle = false,
  className,
}: SkeletonProps) {
  return (
    <div
      className={cn(
        'animate-pulse bg-bg-subtle',
        circle ? 'rounded-full' : 'rounded',
        className
      )}
      style={{
        width: width,
        height: height,
      }}
    />
  );
}

// Pre-configured skeleton variants
export function SkeletonText({ lines = 3 }: { lines?: number }) {
  return (
    <div className="space-y-2">
      {Array.from({ length: lines }).map((_, i) => (
        <Skeleton
          key={i}
          height="1rem"
          className={cn(
            'w-full',
            i === lines - 1 && 'w-3/4' // Last line is shorter
          )}
        />
      ))}
    </div>
  );
}

export function SkeletonCard() {
  return (
    <div className="bg-bg-elevated rounded-lg border border-border p-6 space-y-4">
      <Skeleton height="1.5rem" className="w-1/3" />
      <SkeletonText lines={2} />
      <div className="flex gap-2">
        <Skeleton height="2.5rem" className="w-24" />
        <Skeleton height="2.5rem" className="w-24" />
      </div>
    </div>
  );
}

export function SkeletonInput() {
  return (
    <div className="space-y-2">
      <Skeleton height="0.75rem" className="w-20" />
      <Skeleton height="2.5rem" className="w-full" />
    </div>
  );
}

Skeleton.displayName = 'Skeleton';
