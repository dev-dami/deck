// Package motion is Deck's motion system. Motion is calm and purposeful: it
// runs at up to 60fps only while something is actually moving, costs zero
// ticks at rest, and can be switched off globally (see Reduced).
//
// Components own their springs and fades and report "moving" up to the model;
// the model calls Clock.Wake when anything moves and Clock.Tick after each
// accepted FrameMsg. There is no animation registry.
//
// Every animation Deck uses, and the primitive that drives it:
//
//	working pulse (breathing dot)        Pulse, period 1.6s
//	tool spinner                         Spinner (time-derived, ~8fps, no ticker)
//	streaming cursor                     Pulse / Fade
//	plain -> markdown crossfade          Fade + Blend, 120ms
//	expand / collapse                    Snappy spring on height
//	approval arrival                     2-line slide (Snappy) + Fade on the warning
//	                                     glyph; the badge pulses twice (Pulse)
//	agent switch                         6-col slide + Fade; sidebar bar uses Glide
//	turn complete                        row flash (Fade + Blend); toast fades
//	                                     out (Fade) after 3s
//	@ and / menus                        Soft spring
//	smooth scroll                        Glide spring
//	first-launch wordmark                Typewriter, once, ~400ms
//
// Reduced motion: when Reduced() is true (DECK_REDUCED_MOTION=1, SetReduced,
// or Budget auto-degrade), springs snap to target, fades jump to their end
// value once started, Pulse holds at 1, Spinner shows a static glyph, the
// Typewriter shows its full text and the Clock never ticks.
package motion
