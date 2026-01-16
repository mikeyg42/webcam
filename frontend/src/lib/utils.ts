import { clsx, type ClassValue } from 'clsx';
import { twMerge } from 'tailwind-merge';

/**
 * Combines class names using clsx and tailwind-merge.
 * Handles conditional classes and resolves Tailwind conflicts.
 *
 * @example
 * cn('px-4 py-2', isActive && 'bg-accent', 'px-6') // 'py-2 px-6 bg-accent'
 */
export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}
