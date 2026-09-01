import numpy as np

from vorax_gym import VoraxEnv


env = VoraxEnv("http://127.0.0.1:8080", "vxtrain_replace_me")
observation, info = env.reset(seed=42)
terminated = False
while not terminated:
    legal = np.flatnonzero(observation["action_mask"])
    observation, reward, terminated, truncated, info = env.step(int(np.random.choice(legal)))
print("final score:", info["score"])
env.close()
