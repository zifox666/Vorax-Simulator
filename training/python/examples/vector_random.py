import numpy as np

from vorax_gym import VoraxVectorEnv


env = VoraxVectorEnv("http://127.0.0.1:8080", "vxtrain_replace_me", num_envs=8)
observations, infos = env.reset(seed=100)
while True:
    actions = np.asarray([np.random.choice(np.flatnonzero(mask)) for mask in observations["action_mask"]])
    observations, rewards, terminated, truncated, infos = env.step(actions)
    if terminated.any():
        observations, infos = env.reset(options={"reset_mask": terminated})
