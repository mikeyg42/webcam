import type { Variants, Transition } from 'motion/react';

/**
 * Motion animation variants optimized for compositor-thread performance.
 * All animations use only transform and opacity (no layout/paint triggers).
 */

// Smooth easing curve (CSS cubic-bezier equivalent)
const easeOut: Transition['ease'] = [0.25, 0.1, 0.25, 1];
const easeInOut: Transition['ease'] = [0.4, 0, 0.2, 1];

// Fade in from transparent
export const fadeIn: Variants = {
  hidden: { opacity: 0 },
  visible: {
    opacity: 1,
    transition: { duration: 0.2, ease: easeOut },
  },
  exit: {
    opacity: 0,
    transition: { duration: 0.15, ease: easeOut },
  },
};

// Slide up from below with fade
export const slideUp: Variants = {
  hidden: {
    opacity: 0,
    y: 16,
  },
  visible: {
    opacity: 1,
    y: 0,
    transition: { duration: 0.25, ease: easeOut },
  },
  exit: {
    opacity: 0,
    y: -8,
    transition: { duration: 0.15, ease: easeOut },
  },
};

// Slide in from right (for panels/modals)
export const slideRight: Variants = {
  hidden: {
    opacity: 0,
    x: 20,
  },
  visible: {
    opacity: 1,
    x: 0,
    transition: { duration: 0.25, ease: easeOut },
  },
  exit: {
    opacity: 0,
    x: 20,
    transition: { duration: 0.15, ease: easeOut },
  },
};

// Scale up with fade (for modals/dialogs)
export const scaleUp: Variants = {
  hidden: {
    opacity: 0,
    scale: 0.95,
  },
  visible: {
    opacity: 1,
    scale: 1,
    transition: { duration: 0.2, ease: easeOut },
  },
  exit: {
    opacity: 0,
    scale: 0.95,
    transition: { duration: 0.15, ease: easeOut },
  },
};

// Collapse/expand for accordion sections
// Note: height animation triggers layout, use sparingly
export const collapse: Variants = {
  open: {
    opacity: 1,
    height: 'auto',
    transition: { duration: 0.3, ease: easeInOut },
  },
  closed: {
    opacity: 0,
    height: 0,
    transition: { duration: 0.2, ease: easeInOut },
  },
};

// Button press effect (compositor-safe)
export const buttonPress = {
  whileTap: { scale: 0.97 },
  transition: { duration: 0.1 },
};

// Stagger children animations
export const staggerContainer: Variants = {
  hidden: { opacity: 0 },
  visible: {
    opacity: 1,
    transition: {
      staggerChildren: 0.05,
      delayChildren: 0.1,
    },
  },
};

// Child item for stagger container
export const staggerItem: Variants = {
  hidden: { opacity: 0, y: 12 },
  visible: {
    opacity: 1,
    y: 0,
    transition: { duration: 0.2, ease: easeOut },
  },
};

// Spinner rotation (for loading states)
export const spin: Variants = {
  animate: {
    rotate: 360,
    transition: {
      duration: 1,
      ease: 'linear',
      repeat: Infinity,
    },
  },
};

// Pulse effect (for status indicators)
export const pulse: Variants = {
  animate: {
    scale: [1, 1.05, 1],
    opacity: [1, 0.8, 1],
    transition: {
      duration: 2,
      ease: easeInOut,
      repeat: Infinity,
    },
  },
};

// Tab indicator slide (layoutId animation)
export const tabIndicator = {
  layoutId: 'tab-indicator',
  transition: { type: 'spring', stiffness: 500, damping: 30 },
};
